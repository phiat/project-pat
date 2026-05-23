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
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "layout.html" {
			continue
		}
		full := filepath.Join(r.dir, name)
		if strings.HasPrefix(name, "_") {
			t, err := template.New(name).Funcs(r.funcs).ParseFiles(full)
			if err != nil {
				return fmt.Errorf("parse partial %s: %w", name, err)
			}
			r.partials[strings.TrimSuffix(strings.TrimPrefix(name, "_"), ".html")] = t
			continue
		}
		t, err := template.New("layout.html").Funcs(r.funcs).ParseFiles(layout, full)
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
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute partial %s: %w", partial, err)
	}
	_, err := w.Write(buf.Bytes())
	return err
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
