package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Manager exchanges a long-lived GitHub OAuth token for short-lived Copilot
// tokens and caches them, refreshing automatically before they expire. It is
// safe for concurrent use.
type Manager struct {
	githubToken string
	client      *http.Client

	mu        sync.Mutex
	token     string
	refreshAt time.Time
	expiresAt time.Time
}

// NewManager creates a token manager for the given GitHub OAuth token.
func NewManager(githubToken string, client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{githubToken: githubToken, client: client}
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int    `json:"refresh_in"`
}

// Token returns a valid Copilot token, refreshing it if the cached one is
// missing or past its refresh window.
func (m *Manager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token != "" && time.Now().Before(m.refreshAt) {
		return m.token, nil
	}
	if err := m.refresh(ctx); err != nil {
		// Fall back to a still-valid cached token if the refresh failed but the
		// current token has not actually expired yet.
		if m.token != "" && time.Now().Before(m.expiresAt) {
			return m.token, nil
		}
		return "", err
	}
	return m.token, nil
}

// refresh fetches a new Copilot token. The caller must hold m.mu.
func (m *Manager) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GitHubAPIBaseURL+"/copilot_internal/v2/token", nil)
	if err != nil {
		return err
	}
	setGitHubHeaders(req.Header, m.githubToken)

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("exchanging Copilot token: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("Copilot token exchange rejected (%s); the GitHub login may be expired or lacks Copilot access — run 'copilot-proxy login' again", resp.Status)
		}
		return fmt.Errorf("Copilot token exchange failed (%s): %s", resp.Status, string(data))
	}

	var ct copilotTokenResponse
	if err := json.Unmarshal(data, &ct); err != nil {
		return fmt.Errorf("decoding Copilot token response: %w", err)
	}
	if ct.Token == "" {
		return fmt.Errorf("Copilot token response contained no token")
	}

	now := time.Now()
	m.token = ct.Token
	if ct.ExpiresAt > 0 {
		m.expiresAt = time.Unix(ct.ExpiresAt, 0)
	} else {
		m.expiresAt = now.Add(25 * time.Minute)
	}
	switch {
	case ct.RefreshIn > 0:
		m.refreshAt = now.Add(time.Duration(ct.RefreshIn) * time.Second)
	default:
		// Refresh a minute before expiry if the server didn't tell us when.
		m.refreshAt = m.expiresAt.Add(-1 * time.Minute)
	}
	return nil
}

// Validate performs an initial token exchange to confirm the GitHub token is
// usable, warming the cache in the process.
func (m *Manager) Validate(ctx context.Context) error {
	_, err := m.Token(ctx)
	return err
}
