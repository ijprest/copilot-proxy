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
	srv := New(managerWithToken(t, "copilot-tok", true), log.New(io.Discard, "", 0), log.New(io.Discard, "", 0), false)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("copilot-proxy")) {
		t.Errorf("body = %q, missing service name", rec.Body.String())
	}
}

func TestAccessLogPerRequest(t *testing.T) {
	var buf bytes.Buffer
	access := log.New(&buf, "", 0)
	srv := New(managerWithToken(t, "copilot-tok", true), access, log.New(io.Discard, "", 0), false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	srv.Handler().ServeHTTP(rec, req)

	if got := buf.String(); !strings.Contains(got, "203.0.113.7 GET /healthz") {
		t.Errorf("access log = %q, want it to contain %q", got, "203.0.113.7 GET /healthz")
	}
}

func TestStatusEndpointUnauthenticated(t *testing.T) {
	srv := New(managerWithToken(t, "", false), log.New(io.Discard, "", 0), log.New(io.Discard, "", 0), false)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", rec.Code)
	}
}
