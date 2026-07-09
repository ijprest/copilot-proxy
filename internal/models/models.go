// Package models maps client-supplied model names onto the equivalent GitHub
// Copilot model identifiers. The mapping is hardcoded for now.
package models

// translations maps a client's model name to the Copilot model id.
//
// Claude Code sends Claude versions with a dash between the major and minor
// version (e.g. "claude-opus-4-8"), whereas GitHub Copilot separates them with
// a period (e.g. "claude-opus-4.8"). The Copilot ids below match the GitHub
// Copilot supported-models list. Models with no minor version (such as
// "claude-sonnet-5") are identical on both sides and need no entry.
var translations = map[string]string{
	"claude-opus-4-8":   "claude-opus-4.8",
	"claude-opus-4-7":   "claude-opus-4.7",
	"claude-opus-4-6":   "claude-opus-4.6",
	"claude-opus-4-5":   "claude-opus-4.5",
	"claude-sonnet-4-6": "claude-sonnet-4.6",
	"claude-sonnet-4-5": "claude-sonnet-4.5",
	"claude-haiku-4-5":  "claude-haiku-4.5",
}

// Translate returns the Copilot model id for the given client model name. If no
// mapping is known, the original name is returned and mapped is false.
func Translate(model string) (copilotModel string, mapped bool) {
	if to, ok := translations[model]; ok {
		return to, true
	}
	return model, false
}
