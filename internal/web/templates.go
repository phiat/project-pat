package web

import (
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type Renderer struct {
	dir   string
	funcs template.FuncMap
}

func NewRenderer(dir string) *Renderer {
	return &Renderer{
		dir: dir,
		funcs: template.FuncMap{
			"date":      func(t time.Time) string { return t.Format("2006-01-02 15:04") },
			"truncate":  truncate,
			"upper":     strings.ToUpper,
			"lower":     strings.ToLower,
			"nl2br":     func(s string) template.HTML { return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>")) },
			"add":       func(a, b int) int { return a + b },
			"statusPill": statusPill,
		},
	}
}

func (r *Renderer) Render(w io.Writer, page string, data any) error {
	layout := filepath.Join(r.dir, "layout.html")
	pageFile := filepath.Join(r.dir, page+".html")
	tmpl, err := template.New("layout.html").Funcs(r.funcs).ParseFiles(layout, pageFile)
	if err != nil {
		return fmt.Errorf("parse %s: %w", page, err)
	}
	return tmpl.ExecuteTemplate(w, "layout.html", data)
}

func (r *Renderer) RenderPartial(w io.Writer, partial string, data any) error {
	f := filepath.Join(r.dir, "_"+partial+".html")
	tmpl, err := template.New("_"+partial+".html").Funcs(r.funcs).ParseFiles(f)
	if err != nil {
		return fmt.Errorf("parse partial %s: %w", partial, err)
	}
	return tmpl.Execute(w, data)
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
