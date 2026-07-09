// Package proxy implements the HTTP reverse proxy that forwards a local coding
// agent's OpenAI-style requests to the GitHub Copilot API, injecting the
// authentication and editor headers Copilot requires.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"copilot-proxy/internal/copilot"
	"copilot-proxy/internal/models"
)

type ctxKey int

const tokenKey ctxKey = 0

// Server wraps a reverse proxy to the Copilot API together with the token
// manager used to authenticate each request.
type Server struct {
	tokens  *copilot.Manager
	proxy   *httputil.ReverseProxy
	access  *log.Logger
	errLog  *log.Logger
	verbose bool
}

// New builds a proxy Server targeting the Copilot API. access receives one line
// per incoming request (typically stdout); errLog receives proxy/token errors
// (typically stderr).
func New(tokens *copilot.Manager, access, errLog *log.Logger, verbose bool) *Server {
	target, _ := url.Parse(copilot.CopilotBaseURL)

	s := &Server{tokens: tokens, access: access, errLog: errLog, verbose: verbose}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			token, _ := req.Context().Value(tokenKey).(string)

			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = normalizePath(req.URL.Path)

			// Drop hop-by-hop or client auth headers before setting our own.
			req.Header.Del("Authorization")
			req.Header.Del("X-Request-Id")
			copilot.SetCopilotHeaders(req.Header, token)
		},
		// FlushInterval -1 flushes writes to the client immediately, which is
		// required for streaming (SSE) chat completions to arrive token by token.
		FlushInterval: -1,
		Transport:     newTransport(),
		ErrorLog:      errLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			errLog.Printf("proxy error for %s %s: %v", r.Method, r.URL.Path, err)
			writeJSONError(w, http.StatusBadGateway, "upstream request to Copilot failed: "+err.Error())
		},
	}

	s.proxy = rp
	return s
}

// Handler returns the http.Handler for the proxy.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)
	return mux
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	// Log every incoming request to the access log (stdout).
	s.access.Printf("%s %s %s", clientAddr(r), r.Method, r.URL.RequestURI())

	// A friendly local status page; not forwarded upstream.
	if r.URL.Path == "/" || r.URL.Path == "/healthz" {
		s.handleStatus(w, r)
		return
	}
	s.handleProxy(w, r)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	if _, err := s.tokens.Token(r.Context()); err != nil {
		status = "unauthenticated: " + err.Error()
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"service":  "copilot-proxy",
		"status":   status,
		"upstream": copilot.CopilotBaseURL,
	})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	s.maybeTranslateModel(r)

	token, err := s.tokens.Token(r.Context())
	if err != nil {
		s.errLog.Printf("token error for %s %s: %v", r.Method, r.URL.Path, err)
		writeJSONError(w, http.StatusBadGateway, "could not obtain Copilot token: "+err.Error())
		return
	}

	ctx := context.WithValue(r.Context(), tokenKey, token)
	if s.verbose {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		s.proxy.ServeHTTP(sw, r.WithContext(ctx))
		s.access.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		return
	}
	s.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// maxTranslateBody caps how many bytes of a request body we buffer in order to
// rewrite the model field. Larger bodies are forwarded unchanged.
const maxTranslateBody = 25 << 20 // 25 MiB

// maybeTranslateModel rewrites the top-level "model" field of a JSON request
// body to its Copilot equivalent. Requests without a body, non-JSON bodies,
// bodies with no model, or bodies larger than maxTranslateBody are forwarded
// unchanged.
func (s *Server) maybeTranslateModel(r *http.Request) {
	if r.Body == nil {
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return
	}

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxTranslateBody+1))
	if err != nil || int64(len(buf)) > maxTranslateBody {
		// Could not buffer the whole body safely; forward it as-is by stitching
		// the bytes we already read back onto the remaining stream.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		return
	}
	_ = r.Body.Close()

	out, from, to, changed := rewriteModelBody(buf)
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))
	if changed {
		s.access.Printf("model %q -> %q", from, to)
	}
}

// rewriteModelBody returns body with its top-level "model" field translated to
// the Copilot equivalent. Non-object JSON, a missing model, or an unmapped
// model results in the original bytes being returned unchanged. Only the
// top-level object is re-encoded; all other fields are preserved verbatim.
func rewriteModelBody(body []byte) (out []byte, from, to string, changed bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body, "", "", false
	}
	raw, ok := obj["model"]
	if !ok {
		return body, "", "", false
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return body, "", "", false
	}
	mapped, ok := models.Translate(name)
	if !ok || mapped == name {
		return body, name, name, false
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		return body, name, name, false
	}
	obj["model"] = encoded
	rewritten, err := json.Marshal(obj)
	if err != nil {
		return body, name, name, false
	}
	return rewritten, name, mapped, true
}

// normalizePath maps OpenAI-style paths onto the Copilot API. The Copilot API
// serves endpoints without a "/v1" prefix (e.g. /chat/completions), so a
// leading "/v1" from OpenAI clients is stripped.
func normalizePath(p string) string {
	if p == "/v1" {
		return "/"
	}
	if strings.HasPrefix(p, "/v1/") {
		return p[len("/v1"):]
	}
	return p
}

// clientAddr returns the request's client IP without the ephemeral port.
func clientAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyFromEnvironment
	t.ForceAttemptHTTP2 = true
	return t
}

func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "copilot_proxy_error",
			"code":    code,
		},
	})
}

// statusWriter captures the response status code for verbose logging.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.wroteHeader = true
	}
	return sw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher so streaming responses continue to work when
// the response writer is wrapped for logging.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
