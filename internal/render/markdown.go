package render

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// Markdown renders Markdown to safe HTML. Code blocks get syntax
// highlighting via chroma (inline styles). Raw HTML in the source is
// NOT allowed (prevents XSS); the output is still passed through the
// template system with template.HTML, so only this function's output
// gets trusted.
type Markdown struct {
	md goldmark.Markdown
}

func NewMarkdown() *Markdown {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Linkify,
			extension.Strikethrough,
			highlighting.NewHighlighting(
				highlighting.WithStyle("dracula"),
				highlighting.WithFormatOptions(),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithXHTML(),
			// Unsafe (raw HTML) is OFF by default - keep it that way.
		),
	)
	return &Markdown{md: md}
}

// Render converts raw Markdown text into HTML. Returns an empty string
// on error so callers can fall back to the plain text safely.
func (m *Markdown) Render(raw string) (string, error) {
	var buf bytes.Buffer
	if err := m.md.Convert([]byte(raw), &buf); err != nil {
		return "", fmt.Errorf("markdown render: %w", err)
	}
	return buf.String(), nil
}
