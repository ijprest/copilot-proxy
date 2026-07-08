package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DeviceCode is the response from the device-authorization endpoint.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// RequestDeviceCode starts the OAuth device flow and returns the user code the
// person must enter in their browser.
func RequestDeviceCode(ctx context.Context, client *http.Client) (*DeviceCode, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id": ClientID,
		"scope":     AppScopes,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GitHubBaseURL+"/login/device/code", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (%s): %s", resp.Status, string(data))
	}

	var dc DeviceCode
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("decoding device code response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, fmt.Errorf("incomplete device code response: %s", string(data))
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// PollAccessToken polls GitHub until the user authorizes the device, then
// returns the long-lived GitHub OAuth token.
func PollAccessToken(ctx context.Context, client *http.Client, dc *DeviceCode) (string, error) {
	interval := time.Duration(dc.Interval+1) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return "", fmt.Errorf("device code expired before authorization was completed")
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		tok, err := requestAccessToken(ctx, client, dc.DeviceCode)
		if err != nil {
			return "", err
		}
		switch {
		case tok.AccessToken != "":
			return tok.AccessToken, nil
		case tok.Error == "authorization_pending":
			// Keep waiting.
		case tok.Error == "slow_down":
			interval += 5 * time.Second
		case tok.Error == "":
			// No token yet and no error; keep waiting.
		default:
			msg := tok.Error
			if tok.ErrorDesc != "" {
				msg = tok.ErrorDesc
			}
			return "", fmt.Errorf("authorization failed: %s", msg)
		}
	}
}

func requestAccessToken(ctx context.Context, client *http.Client, deviceCode string) (*accessTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id":   ClientID,
		"device_code": deviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GitHubBaseURL+"/login/oauth/access_token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polling access token: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var out accessTokenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding access token response (%s): %w", resp.Status, err)
	}
	return &out, nil
}
