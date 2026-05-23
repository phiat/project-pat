package handlers

import (
	"strings"
	"testing"
)

func TestExtractFencedJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"simple", "before\n```json\n{\"a\":1}\n```\nafter", `{"a":1}`},
		{"no closing fence", "```json\n{\"a\":1}\n", ""},
		{"no opener", "{\"a\":1}", ""},
		{"prose only", "no json here", ""},
		{"two fences, first wins", "```json\n{\"a\":1}\n```\nthen ```json\n{\"b\":2}\n```", `{"a":1}`},
		{"empty fenced block", "```json\n\n```", ""},
	}
	for _, c := range cases {
		got := extractFencedJSON(c.in)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestExtractFirstObject(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", `prose {"a":1} more`, `{"a":1}`},
		{"nested", `{"a":{"b":2},"c":[1,2,3]}`, `{"a":{"b":2},"c":[1,2,3]}`},
		{"brace in string", `{"name":"has } inside","x":1}`, `{"name":"has } inside","x":1}`},
		{"escaped quote in string", `{"q":"he said \"hi\"","n":3}`, `{"q":"he said \"hi\"","n":3}`},
		{"no object", "just prose with no braces", ""},
		{"unbalanced", `{"a":1`, ""},
	}
	for _, c := range cases {
		got := extractFirstObject(c.in)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestParseClustererJSON(t *testing.T) {
	good := "Here are themes I see...\n\n```json\n" + `{
  "clusters": [
    {"label": "rendering", "idea_ids": [1, 3]},
    {"label": "agents", "idea_ids": [2, 4]}
  ],
  "edges": [
    {"a": 1, "b": 3, "weight": 0.7, "reason": "both gpu"}
  ]
}` + "\n```"
	res, err := parseClustererJSON(good)
	if err != nil {
		t.Fatalf("good: unexpected err: %v", err)
	}
	if len(res.clusters) != 2 {
		t.Errorf("clusters: got %d want 2", len(res.clusters))
	}
	if len(res.links) != 1 {
		t.Errorf("links: got %d want 1", len(res.links))
	}
	if res.clusters[0].Label != "rendering" {
		t.Errorf("first cluster label: %q", res.clusters[0].Label)
	}
	if res.links[0].Weight != 0.7 {
		t.Errorf("link weight: %v", res.links[0].Weight)
	}

	// fallback: unfenced object after prose
	plain := "(thoughts) " + `{"clusters":[],"edges":[]}`
	if _, err := parseClustererJSON(plain); err != nil {
		t.Errorf("plain object fallback: %v", err)
	}

	// failure: empty
	if _, err := parseClustererJSON("nothing here"); err == nil {
		t.Errorf("empty input should error")
	}

	// failure: malformed json inside fence
	bad := "```json\n{not valid}\n```"
	if _, err := parseClustererJSON(bad); err == nil {
		t.Errorf("malformed json should error")
	}

	// brace-in-string survives extractFirstObject
	tricky := `noise {"clusters":[{"label":"a}b","idea_ids":[1]}],"edges":[]}`
	res, err = parseClustererJSON(tricky)
	if err != nil {
		t.Fatalf("brace-in-string: %v", err)
	}
	if len(res.clusters) != 1 || res.clusters[0].Label != "a}b" {
		t.Errorf("brace-in-string: clusters wrong: %+v", res.clusters)
	}

	// fenced wins over later prose-object
	mixed := "noise " + "```json\n" + `{"clusters":[{"label":"in-fence","idea_ids":[1]}],"edges":[]}` + "\n```\n" + `{"clusters":[{"label":"after","idea_ids":[2]}],"edges":[]}`
	res, err = parseClustererJSON(mixed)
	if err != nil {
		t.Fatalf("mixed: %v", err)
	}
	if len(res.clusters) != 1 || !strings.Contains(res.clusters[0].Label, "fence") {
		t.Errorf("mixed: should prefer fenced block: %+v", res.clusters)
	}
}
