package handlers

import (
	"strings"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantP  string
		wantOK bool
	}{
		{"clean file", "main.go", "main.go", true},
		{"nested", "cmd/server/main.go", "cmd/server/main.go", true},
		{"with dot", "./main.go", "main.go", true},
		{"empty", "", "", false},
		{"whitespace only", "   ", "", false},
		{"leading slash", "/etc/passwd", "", false},
		{"absolute root", "/", "", false},
		{"traversal", "../escape", "", false},
		{"traversal nested", "foo/../../escape", "", false},
		{"single dotdot", "..", "", false},
		{"single dot", ".", "", false},
		{"backslash", "win\\path", "", false},
		{"nul byte", "foo\x00bar", "", false},
		{"overlong", strings.Repeat("a", 250), "", false},
		// Note: foo/./bar collapses to foo/bar via filepath.Clean — allowed.
		{"redundant dot", "foo/./bar.txt", "foo/bar.txt", true},
	}
	for _, c := range cases {
		got, ok := sanitizeRelPath(c.in)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v (got=%q)", c.name, ok, c.wantOK, got)
		}
		if ok && got != c.wantP {
			t.Errorf("%s: path=%q want %q", c.name, got, c.wantP)
		}
	}
}

func TestParsePrototypeJSON(t *testing.T) {
	good := "preamble\n```json\n" + `{
        "files": [
          {"path": "main.go", "content": "package main\n"},
          {"path": "README.md", "content": "# proto"}
        ]
      }` + "\n```"
	files, err := parsePrototypeJSON(good)
	if err != nil {
		t.Fatalf("good: %v", err)
	}
	if len(files) != 2 || files[0].Path != "main.go" {
		t.Errorf("files wrong: %+v", files)
	}

	// no JSON at all
	if _, err := parsePrototypeJSON("just prose"); err == nil {
		t.Errorf("expected parse error on plain prose")
	}

	// empty files list
	empty := "```json\n" + `{"files": []}` + "\n```"
	if _, err := parsePrototypeJSON(empty); err == nil {
		t.Errorf("expected error on empty files")
	}

	// way too many files — refuse
	var b strings.Builder
	b.WriteString("```json\n{\"files\":[")
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"path":"f.txt","content":""}`)
	}
	b.WriteString("]}\n```")
	if _, err := parsePrototypeJSON(b.String()); err == nil {
		t.Errorf("expected error on >16 files")
	}

	// unfenced fallback
	unfenced := `noise {"files":[{"path":"a","content":"b"}]}`
	if _, err := parsePrototypeJSON(unfenced); err != nil {
		t.Errorf("unfenced fallback failed: %v", err)
	}
}
