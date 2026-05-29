package llm

import (
	"strings"
	"testing"
)

func TestResolveBaseURL(t *testing.T) {
	const def = "https://api.example.com/v1"

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"empty falls back to default", "", def, ""},
		{"whitespace falls back", "   ", def, ""},
		{"https accepted", "https://api.openai.com/v1", "https://api.openai.com/v1", ""},
		{"http accepted (local ollama)", "http://localhost:11434/v1", "http://localhost:11434/v1", ""},
		{"trailing slash trimmed", "https://api.openai.com/v1/", "https://api.openai.com/v1", ""},
		{"file scheme rejected", "file:///etc/passwd", "", "scheme"},
		{"gopher rejected", "gopher://host/path", "", "scheme"},
		{"javascript scheme rejected", "javascript:alert(1)", "", "scheme"},
		{"missing host rejected", "https:///", "", "host"},
		// unparseable URL — url.Parse is permissive so we mostly get errors
		// via scheme/host checks; this one has a control char which Parse
		// rejects.
		{"control chars rejected", "https://api.openai.com\x7f/v1", "", ""},
	}
	for _, c := range cases {
		got, err := resolveBaseURL(c.in, def)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: want err containing %q, got %v", c.name, c.wantErr, err)
			}
			continue
		}
		if c.in == "https://api.openai.com\x7f/v1" {
			// either an error (preferred) or the cleaned URL — just require
			// that we did NOT pass through the control char.
			if err == nil && strings.Contains(got, "\x7f") {
				t.Errorf("%s: control char passed through: %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got=%q want=%q", c.name, got, c.want)
		}
	}
}

func TestNewUnknownProvider(t *testing.T) {
	_, err := New(Opts{Provider: "made-up", APIKey: "k"})
	if err == nil {
		t.Fatalf("expected unknown-provider error")
	}
	if !strings.Contains(err.Error(), "made-up") {
		t.Errorf("error should name the bad provider: %v", err)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	// SSRF guard: file:// passed via opts.BaseURL must surface as an error
	// from New(), not silently become the runtime base URL.
	_, err := New(Opts{Provider: "openai", APIKey: "k", BaseURL: "file:///etc/passwd"})
	if err == nil {
		t.Fatalf("expected error on file:// base URL")
	}
	if !strings.Contains(err.Error(), "LLM_BASE_URL") {
		t.Errorf("error should mention the env name: %v", err)
	}
}
