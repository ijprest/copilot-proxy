package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"copilot-proxy/internal/copilot"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions": "/chat/completions",
		"/chat/completions":    "/chat/completions",
		"/v1":                  "/",
		"/v1/models":           "/models",
		"/models":              "/models",
		"/v1beta/x":            "/v1beta/x",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// managerWithToken returns a Manager that yields a Copilot token via a stubbed
// HTTP client, avoiding any network access.
func managerWithToken(t *testing.T, token string, ok bool) *copilot.Manager {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !ok {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		body, _ := json.Marshal(map[string]any{
			"token":      token,
			"expires_at": time.Now().Add(time.Hour).Unix(),
		})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	return copilot.NewManager("gh-token", client)
}

func TestStatusEndpointAuthenticated(t *testing.T) {
	srv := New(
		managerWithToken(t, "copilot-tok", true),
		log.New(io.Discard, "", 0),
		log.New(io.Discard, "", 0),
		false,
		[]string{"claude-sonnet-5", "gpt-5.5"},
	)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
	for _, want := range []string{"copilot-proxy", "Healthy", "claude-sonnet-5", "gpt-5.5", "2 total"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAccessLogPerRequest(t *testing.T) {
	var buf bytes.Buffer
	access := log.New(&buf, "", 0)
	srv := New(managerWithToken(t, "copilot-tok", true), access, log.New(io.Discard, "", 0), false, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	srv.Handler().ServeHTTP(rec, req)

	if got := buf.String(); !strings.Contains(got, "203.0.113.7 GET /healthz") {
		t.Errorf("access log = %q, want it to contain %q", got, "203.0.113.7 GET /healthz")
	}
}

func TestStatusEndpointUnauthenticated(t *testing.T) {
	srv := New(
		managerWithToken(t, "", false),
		log.New(io.Discard, "", 0),
		log.New(io.Discard, "", 0),
		false,
		[]string{"gpt-5.5"},
	)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", rec.Code)
	}
	for _, want := range []string{"Unavailable", "Authentication failed", "gpt-5.5"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestStatusEndpointEscapesModelIDs(t *testing.T) {
	srv := New(
		managerWithToken(t, "copilot-tok", true),
		log.New(io.Discard, "", 0),
		log.New(io.Discard, "", 0),
		false,
		[]string{`<script>alert("x")</script>`},
	)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("model id was not HTML-escaped")
	}
	if !strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Error("escaped model id missing from status page")
	}
}

func TestRewriteModelBody(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	out, from, to, changed := rewriteModelBody(body)
	if !changed || from != "claude-opus-4-8" || to != "claude-opus-4.8" {
		t.Fatalf("changed=%v from=%q to=%q", changed, from, to)
	}

	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if obj["model"] != "claude-opus-4.8" {
		t.Errorf("model = %v, want claude-opus-4.8", obj["model"])
	}
	// Unrelated fields must survive the rewrite.
	if obj["stream"] != true {
		t.Errorf("stream field lost: %v", obj["stream"])
	}
	if msgs, ok := obj["messages"].([]any); !ok || len(msgs) != 1 {
		t.Errorf("messages field lost: %v", obj["messages"])
	}
}

func TestRewriteModelBodyPassthrough(t *testing.T) {
	cases := map[string][]byte{
		"unmapped model": []byte(`{"model":"gpt-5.5"}`),
		"no model":       []byte(`{"messages":[]}`),
		"not JSON":       []byte(`not json at all`),
		"JSON array":     []byte(`[1,2,3]`),
	}
	for name, body := range cases {
		out, _, _, changed := rewriteModelBody(body)
		if changed {
			t.Errorf("%s: unexpectedly changed", name)
		}
		if string(out) != string(body) {
			t.Errorf("%s: body altered: got %q want %q", name, out, body)
		}
	}
}

func TestMaybeTranslateModelRewritesRequest(t *testing.T) {
	srv := New(managerWithToken(t, "tok", true), log.New(io.Discard, "", 0), log.New(io.Discard, "", 0), false, nil)

	body := `{"model":"claude-haiku-4-5","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.maybeTranslateModel(req)

	got, _ := io.ReadAll(req.Body)
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("rewritten body not valid JSON: %v", err)
	}
	if obj["model"] != "claude-haiku-4.5" {
		t.Errorf("model = %v, want claude-haiku-4.5", obj["model"])
	}
	if req.ContentLength != int64(len(got)) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(got))
	}
}

func TestMaybeTranslateModelSkipsGET(t *testing.T) {
	srv := New(managerWithToken(t, "tok", true), log.New(io.Discard, "", 0), log.New(io.Discard, "", 0), false, nil)
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	srv.maybeTranslateModel(req) // must not panic on a bodyless request
}
