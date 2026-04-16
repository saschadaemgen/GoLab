package render

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Sanitizer is GoLab's single HTML sanitization policy. It is applied to
// all user-generated HTML (Quill output, Markdown-rendered HTML, etc.)
// before storing or displaying. The policy is permissive enough to let
// the rich editor keep its formatting, but strips every script tag,
// event handler, javascript: URL and dangerous element.
type Sanitizer struct {
	policy *bluemonday.Policy
}

func NewSanitizer() *Sanitizer {
	p := bluemonday.UGCPolicy()

	// Allow Quill / Chroma / highlight.js class names on code blocks and spans.
	p.AllowAttrs("class").
		Matching(regexp.MustCompile(`^(ql-|language-|hljs|chroma|code-block-wrapper)([\w\- ]*)$`)).
		OnElements("pre", "code", "span", "div", "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote")

	// Images must come from our own upload endpoint. Reject any other src.
	p.AllowAttrs("src").
		Matching(regexp.MustCompile(`^/static/uploads/`)).
		OnElements("img")
	p.AllowAttrs("alt").OnElements("img")

	// Permit Quill's data-language attribute on pre for syntax highlighting.
	p.AllowDataAttributes()

	// Common safe HTML elements that UGCPolicy already allows, but we want
	// to be explicit about headings for Quill's output.
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")

	return &Sanitizer{policy: p}
}

// Clean returns sanitized HTML suitable for rendering as template.HTML.
func (s *Sanitizer) Clean(raw string) string {
	return s.policy.Sanitize(raw)
}

// LooksLikeHTML returns true when `s` is likely rich HTML from Quill.
// We keep the existing Markdown pipeline alive for old posts and any
// API client that still submits plain text/Markdown.
func LooksLikeHTML(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	if !strings.HasPrefix(trimmed, "<") {
		return false
	}
	// Look for a closing tag anywhere - plain "<foo" isn't valid HTML.
	return strings.Contains(trimmed, ">")
}
