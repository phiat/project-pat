package web

import (
	"bytes"
	"html/template"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	mdOnce sync.Once
	md     goldmark.Markdown
)

func mdRenderer() goldmark.Markdown {
	mdOnce.Do(func() {
		md = goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithHardWraps()),
		)
	})
	return md
}

// RenderMarkdown converts a markdown source into HTML. Goldmark escapes raw
// HTML by default, so returning template.HTML is safe.
func RenderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := mdRenderer().Convert([]byte(src), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	return template.HTML(buf.String())
}
