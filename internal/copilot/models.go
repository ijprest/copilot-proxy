package copilot

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// FetchModels queries the Copilot /models endpoint using a valid Copilot token
// and returns the raw response body. A non-2xx status is returned as an error
// along with whatever body was received (useful for diagnostics).
func FetchModels(ctx context.Context, m *Manager, client *http.Client) ([]byte, error) {
	token, err := m.Token(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotBaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	SetCopilotHeaders(req.Header, token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting models: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, fmt.Errorf("models request failed (%s)", resp.Status)
	}
	return data, nil
}
