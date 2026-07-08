package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc stubs HTTP responses so the token exchange can be tested
// without touching the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, v any) *http.Response {
	body, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestSetCopilotHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer client-supplied")
	SetCopilotHeaders(h, "tok123")

	if got := h.Get("Authorization"); got != "Bearer tok123" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok123")
	}
	want := map[string]string{
		"Copilot-Integration-Id": "vscode-chat",
		"Openai-Intent":          "conversation-panel",
		"X-Github-Api-Version":   apiVersion,
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if h.Get("Editor-Version") == "" {
		t.Error("Editor-Version not set")
	}
	if h.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id not set")
	}
}

func TestNewUUIDFormat(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := newUUID(), newUUID()
	if !re.MatchString(a) {
		t.Fatalf("newUUID() = %q, not a v4 UUID", a)
	}
	if a == b {
		t.Fatal("two UUIDs should not be equal")
	}
}

func TestManagerTokenCachesAndRefreshes(t *testing.T) {
	var calls int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/copilot_internal/v2/token") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "token gh-token" {
			t.Errorf("Authorization = %q, want %q", got, "token gh-token")
		}
		n := atomic.AddInt32(&calls, 1)
		return jsonResponse(http.StatusOK, copilotTokenResponse{
			Token:     fmt.Sprintf("copilot-%d", n),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}), nil
	})}

	m := NewManager("gh-token", client)

	tok, err := m.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if tok != "copilot-1" {
		t.Fatalf("first token = %q, want copilot-1", tok)
	}

	// Second call should be served from cache (no new exchange).
	tok, err = m.Token(context.Background())
	if err != nil {
		t.Fatalf("cached Token: %v", err)
	}
	if tok != "copilot-1" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("cache miss: token=%q calls=%d", tok, calls)
	}

	// Force the refresh window to elapse and expect a fresh token.
	m.mu.Lock()
	m.refreshAt = time.Now().Add(-time.Minute)
	m.mu.Unlock()

	tok, err = m.Token(context.Background())
	if err != nil {
		t.Fatalf("refresh Token: %v", err)
	}
	if tok != "copilot-2" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected refresh: token=%q calls=%d", tok, calls)
	}
}

func TestManagerTokenAuthError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("bad credentials")),
			Header:     make(http.Header),
		}, nil
	})}

	m := NewManager("bad-token", client)
	if _, err := m.Token(context.Background()); err == nil {
		t.Fatal("expected error for unauthorized token exchange")
	}
}
