// Package proxy implements the HTTP reverse proxy that forwards a local coding
// agent's OpenAI-style requests to the GitHub Copilot API, injecting the
// authentication and editor headers Copilot requires.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
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

var statusPage = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>copilot-proxy status</title>
  <style>
    :root {
      color-scheme: dark;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #090d14;
      color: #e6edf3;
    }
    * { box-sizing: border-box; }
    body {
      min-height: 100vh;
      margin: 0;
      background:
        radial-gradient(circle at 15% 0%, rgba(46, 160, 67, .18), transparent 32rem),
        radial-gradient(circle at 100% 25%, rgba(88, 166, 255, .12), transparent 28rem),
        #090d14;
    }
    body.unhealthy {
      background:
        radial-gradient(circle at 15% 0%, rgba(248, 81, 73, .18), transparent 32rem),
        radial-gradient(circle at 100% 25%, rgba(88, 166, 255, .10), transparent 28rem),
        #090d14;
    }
    main {
      width: min(960px, calc(100% - 2rem));
      margin: 0 auto;
      padding: clamp(3rem, 8vw, 6rem) 0;
    }
    header {
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 2rem;
      margin-bottom: 2rem;
    }
    .eyebrow {
      margin: 0 0 .5rem;
      color: #8b949e;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: .8rem;
      letter-spacing: .12em;
      text-transform: uppercase;
    }
    h1 {
      margin: 0;
      font-size: clamp(2rem, 6vw, 3.75rem);
      letter-spacing: -.04em;
      line-height: 1.05;
    }
    .lede {
      max-width: 42rem;
      margin: 1rem 0 0;
      color: #8b949e;
      font-size: 1.05rem;
      line-height: 1.6;
    }
    .status {
      display: inline-flex;
      flex: 0 0 auto;
      align-items: center;
      gap: .55rem;
      padding: .5rem .8rem;
      border: 1px solid rgba(46, 160, 67, .45);
      border-radius: 999px;
      background: rgba(46, 160, 67, .12);
      color: #7ee787;
      font-size: .85rem;
      font-weight: 700;
    }
    .status::before {
      width: .55rem;
      height: .55rem;
      border-radius: 50%;
      background: currentColor;
      box-shadow: 0 0 0 .25rem rgba(46, 160, 67, .16);
      content: "";
    }
    .unhealthy .status {
      border-color: rgba(248, 81, 73, .45);
      background: rgba(248, 81, 73, .12);
      color: #ff7b72;
    }
    .unhealthy .status::before { box-shadow: 0 0 0 .25rem rgba(248, 81, 73, .16); }
    .grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 1rem;
      margin-bottom: 1rem;
    }
    .card {
      border: 1px solid #30363d;
      border-radius: 14px;
      background: rgba(13, 17, 23, .78);
      box-shadow: 0 16px 40px rgba(0, 0, 0, .2);
    }
    .metric { padding: 1.25rem; }
    .label {
      display: block;
      margin-bottom: .55rem;
      color: #8b949e;
      font-size: .78rem;
      font-weight: 600;
      letter-spacing: .06em;
      text-transform: uppercase;
    }
    .metric strong, .metric code {
      display: block;
      overflow-wrap: anywhere;
      color: #e6edf3;
      font-size: 1rem;
    }
    .models { padding: 1.25rem; }
    .models-heading {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 1rem;
      margin-bottom: 1rem;
    }
    h2 { margin: 0; font-size: 1.1rem; }
    .count {
      padding: .25rem .55rem;
      border-radius: 999px;
      background: #21262d;
      color: #8b949e;
      font-size: .75rem;
      font-weight: 600;
    }
    ul {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      gap: .65rem;
      margin: 0;
      padding: 0;
      list-style: none;
    }
    li {
      min-width: 0;
      padding: .75rem;
      border: 1px solid #30363d;
      border-radius: 9px;
      background: #161b22;
    }
    li code {
      color: #d2a8ff;
      overflow-wrap: anywhere;
    }
    .empty {
      margin: 0;
      padding: 1rem;
      border: 1px dashed #30363d;
      border-radius: 9px;
      color: #8b949e;
      text-align: center;
    }
    code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
    @media (max-width: 620px) {
      header { flex-direction: column-reverse; gap: 1.25rem; }
      .grid { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body class="{{if .Healthy}}healthy{{else}}unhealthy{{end}}">
  <main>
    <header>
      <div>
        <p class="eyebrow">copilot-proxy</p>
        <h1>{{if .Healthy}}Ready to proxy{{else}}Needs attention{{end}}</h1>
        <p class="lede">{{.Status}}</p>
      </div>
      <span class="status">{{if .Healthy}}Healthy{{else}}Unavailable{{end}}</span>
    </header>

    <section class="grid" aria-label="Service details">
      <article class="card metric">
        <span class="label">Upstream</span>
        <code>{{.Upstream}}</code>
      </article>
      <article class="card metric">
        <span class="label">Models discovered at startup</span>
        <strong>{{len .Models}}</strong>
      </article>
    </section>

    <section class="card models">
      <div class="models-heading">
        <h2>Available models</h2>
        <span class="count">{{len .Models}} total</span>
      </div>
      {{if .Models}}
      <ul>
        {{range .Models}}<li><code>{{.}}</code></li>{{end}}
      </ul>
      {{else}}
      <p class="empty">No model IDs were collected during startup.</p>
      {{end}}
    </section>
  </main>
</body>
</html>
`))

type statusPageData struct {
	Healthy  bool
	Status   string
	Upstream string
	Models   []string
}

// Server wraps a reverse proxy to the Copilot API together with the token
// manager used to authenticate each request.
type Server struct {
	tokens  *copilot.Manager
	proxy   *httputil.ReverseProxy
	access  *log.Logger
	errLog  *log.Logger
	verbose bool
	models  []string
}

// New builds a proxy Server targeting the Copilot API. access receives one line
// per incoming request (typically stdout); errLog receives proxy/token errors
// (typically stderr).
func New(tokens *copilot.Manager, access, errLog *log.Logger, verbose bool, models []string) *Server {
	target, _ := url.Parse(copilot.CopilotBaseURL)

	s := &Server{
		tokens:  tokens,
		access:  access,
		errLog:  errLog,
		verbose: verbose,
		models:  append([]string(nil), models...),
	}

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
	healthy := true
	status := "Authenticated and ready to forward requests to GitHub Copilot."
	code := http.StatusOK
	if _, err := s.tokens.Token(r.Context()); err != nil {
		healthy = false
		status = "Authentication failed: " + err.Error()
		code = http.StatusServiceUnavailable
	}

	var body bytes.Buffer
	if err := statusPage.Execute(&body, statusPageData{
		Healthy:  healthy,
		Status:   status,
		Upstream: copilot.CopilotBaseURL,
		Models:   s.models,
	}); err != nil {
		s.errLog.Printf("rendering status page: %v", err)
		http.Error(w, "could not render status page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write(body.Bytes())
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
