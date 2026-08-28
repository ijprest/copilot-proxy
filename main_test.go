package main

import (
	"reflect"
	"testing"
)

func TestModelIDsFromResponse(t *testing.T) {
	data := []byte(`{
		"data": [
			{"id": "gpt-5.5"},
			{"id": ""},
			{"id": "claude-sonnet-5"}
		]
	}`)

	got := modelIDsFromResponse(data)
	want := []string{"claude-sonnet-5", "gpt-5.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modelIDsFromResponse() = %v, want %v", got, want)
	}
}

func TestModelIDsFromInvalidResponse(t *testing.T) {
	if got := modelIDsFromResponse([]byte(`not JSON`)); got != nil {
		t.Errorf("modelIDsFromResponse() = %v, want nil", got)
	}
}
