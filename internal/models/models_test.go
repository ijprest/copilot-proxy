package models

import "testing"

func TestTranslate(t *testing.T) {
	cases := []struct {
		in         string
		want       string
		wantMapped bool
	}{
		{"claude-opus-4-8", "claude-opus-4.8", true},
		{"claude-opus-4-5", "claude-opus-4.5", true},
		{"claude-haiku-4-5", "claude-haiku-4.5", true},
		{"claude-sonnet-4-6", "claude-sonnet-4.6", true},
		{"claude-sonnet-5", "claude-sonnet-5", false}, // no minor version; identical
		{"gpt-5.5", "gpt-5.5", false},                 // unknown; passthrough
		{"", "", false},
	}
	for _, c := range cases {
		got, mapped := Translate(c.in)
		if got != c.want || mapped != c.wantMapped {
			t.Errorf("Translate(%q) = (%q, %v), want (%q, %v)", c.in, got, mapped, c.want, c.wantMapped)
		}
	}
}
