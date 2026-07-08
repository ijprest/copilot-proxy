// Package copilot implements the GitHub authentication flow and short-lived
// Copilot token management needed to talk to the Copilot API on a user's behalf.
package copilot

import (
	"crypto/rand"
	"fmt"
	"net/http"
)

// Well-known endpoints and client identifiers used by the GitHub Copilot
// clients. These are the same values the official editor integrations use for
// the OAuth device flow and token exchange.
const (
	GitHubBaseURL    = "https://github.com"
	GitHubAPIBaseURL = "https://api.github.com"
	CopilotBaseURL   = "https://api.githubcopilot.com"

	// ClientID is the public OAuth client id used by the Copilot editor
	// integrations for the device-authorization flow.
	ClientID = "Iv1.b507a08c87ecfe98"
	// AppScopes are the OAuth scopes requested during login.
	AppScopes = "read:user"

	copilotVersion      = "0.26.7"
	editorVersion       = "vscode/1.99.0"
	editorPluginVersion = "copilot-chat/" + copilotVersion
	userAgent           = "GitHubCopilotChat/" + copilotVersion
	apiVersion          = "2025-04-01"
)

// setGitHubHeaders sets the headers used when calling api.github.com to
// exchange a GitHub OAuth token for a short-lived Copilot token.
func setGitHubHeaders(h http.Header, githubToken string) {
	h.Set("Accept", "application/json")
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "token "+githubToken)
	h.Set("Editor-Version", editorVersion)
	h.Set("Editor-Plugin-Version", editorPluginVersion)
	h.Set("User-Agent", userAgent)
	h.Set("X-GitHub-Api-Version", apiVersion)
}

// SetCopilotHeaders sets the headers required by the Copilot API. Any
// client-supplied Authorization header is replaced with the Copilot token.
func SetCopilotHeaders(h http.Header, copilotToken string) {
	h.Set("Authorization", "Bearer "+copilotToken)
	h.Set("Copilot-Integration-Id", "vscode-chat")
	h.Set("Editor-Version", editorVersion)
	h.Set("Editor-Plugin-Version", editorPluginVersion)
	h.Set("User-Agent", userAgent)
	h.Set("Openai-Intent", "conversation-panel")
	h.Set("X-GitHub-Api-Version", apiVersion)
	h.Set("X-Request-Id", newUUID())
	h.Set("X-VSCode-User-Agent-Library-Version", "electron-fetch")
}

// newUUID returns a random RFC 4122 version 4 UUID string. It relies only on
// crypto/rand from the standard library to avoid external dependencies.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read should never fail; fall back to an all-zero UUID.
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
