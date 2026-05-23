package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"projectpat/internal/stack"
)

type Renderer struct {
	dir      string
	funcs    template.FuncMap
	pages    map[string]*template.Template
	partials map[string]*template.Template
}

func NewRenderer(dir string) (*Renderer, error) {
	r := &Renderer{
		dir: dir,
		funcs: template.FuncMap{
			"date":       func(t time.Time) string { return t.Format("2006-01-02 15:04") },
			"truncate":   truncate,
			"upper":      strings.ToUpper,
			"lower":      strings.ToLower,
			"nl2br":      func(s string) template.HTML { return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>")) },
			"add":        func(a, b int) int { return a + b },
			"statusPill": statusPill,
			"safeHTML":   func(s string) template.HTML { return template.HTML(s) },
			"markdown":   RenderMarkdown,
			"compatible": stack.IsOptionCompatible,
			"copyBtn":    copyButtonHTML,
			"dict":       dictHelper,
		},
		pages:    make(map[string]*template.Template),
		partials: make(map[string]*template.Template),
	}
	if err := r.loadAll(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Renderer) loadAll() error {
	layout := filepath.Join(r.dir, "layout.html")
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read templates dir %s: %w", r.dir, err)
	}
	var partialPaths []string
	var pagePaths []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "layout.html" {
			continue
		}
		full := filepath.Join(r.dir, name)
		if strings.HasPrefix(name, "_") {
			partialPaths = append(partialPaths, full)
		} else {
			pagePaths = append(pagePaths, full)
		}
	}
	// each partial standalone for RenderPartial
	for _, full := range partialPaths {
		name := filepath.Base(full)
		t, err := template.New(name).Funcs(r.funcs).ParseFiles(full)
		if err != nil {
			return fmt.Errorf("parse partial %s: %w", name, err)
		}
		r.partials[strings.TrimSuffix(strings.TrimPrefix(name, "_"), ".html")] = t
	}
	// each page = layout + that page + ALL partials, so pages can `{{template "name" .}}`
	for _, full := range pagePaths {
		name := filepath.Base(full)
		files := append([]string{layout, full}, partialPaths...)
		t, err := template.New("layout.html").Funcs(r.funcs).ParseFiles(files...)
		if err != nil {
			return fmt.Errorf("parse page %s: %w", name, err)
		}
		r.pages[strings.TrimSuffix(name, ".html")] = t
	}
	if _, ok := r.pages["home"]; !ok {
		return fmt.Errorf("missing home template in %s", r.dir)
	}
	return nil
}

func (r *Renderer) Render(w io.Writer, page string, data any) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown page %q", page)
	}
	// buffer so a render error doesn't leave a half-written response
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		return fmt.Errorf("execute %s: %w", page, err)
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func (r *Renderer) RenderPartial(w io.Writer, partial string, data any) error {
	t, ok := r.partials[partial]
	if !ok {
		return fmt.Errorf("unknown partial %q", partial)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, partial, data); err != nil {
		return fmt.Errorf("execute partial %s: %w", partial, err)
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// dictHelper builds a map from alternating key/value args, for inline
// template literals like: {{template "x" (dict "Key" "val" "N" 5)}}.
func dictHelper(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of args")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d not a string", i)
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

// copyButtonHTML returns a small copy button bound to either a
// data-source element ("source:<name>") or a target by id ("target:<id>")
// or a CSS selector. Use {{copyBtn "source:doc"}}, {{copyBtn "target:foo"}},
// or {{copyBtn "sel:.something"}}.
func copyButtonHTML(spec string) template.HTML {
	kind, val := "source", spec
	if i := strings.IndexByte(spec, ':'); i > 0 {
		kind, val = spec[:i], spec[i+1:]
	}
	var attr string
	switch kind {
	case "source":
		attr = `data-copy-source-id="copy-src-` + template.HTMLEscapeString(val) + `"`
	case "target":
		attr = `data-copy-target="` + template.HTMLEscapeString(val) + `"`
	case "sel":
		attr = `data-copy-source="` + template.HTMLEscapeString(val) + `"`
	default:
		attr = `data-copy-source-id="copy-src-` + template.HTMLEscapeString(spec) + `"`
	}
	return template.HTML(`<button class="copy-btn" type="button" aria-label="copy" title="copy" ` + attr + `>` +
		`<svg class="copy-icon-default" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>` +
		`<svg class="copy-icon-done" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>` +
		`</button>`)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func statusPill(status string) template.HTML {
	cls := "pill"
	switch status {
	case "running", "drafting":
		cls += " pill-active"
	case "ok", "done", "shipped":
		cls += " pill-ok"
	case "error", "failed":
		cls += " pill-err"
	default:
		cls += " pill-muted"
	}
	return template.HTML(`<span class="` + cls + `">` + template.HTMLEscapeString(status) + `</span>`)
}
