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

	// Images are allowed either from our own upload endpoint OR from a
	// small allowlist of GIF hosts so users can paste GIF URLs from
	// Tenor, Giphy, Imgur, Reddit, etc. without a third-party API.
	// Tenor/Giphy branding URLs stay valid even after their API
	// shutdowns because the static CDNs remain public.
	p.AllowAttrs("src").
		Matching(regexp.MustCompile(
			`^(/static/uploads/` +
				`|https://media\d?\.tenor\.com/` +
				`|https://media\d?\.giphy\.com/` +
				`|https://i\.giphy\.com/` +
				`|https://i\.imgur\.com/` +
				`|https://i\.redd\.it/` +
				`).+`,
		)).
		OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")

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

// stripAllPolicy is bluemonday's StrictPolicy: removes every tag,
// leaves only text content. Used by IsSemanticallyEmpty below to
// turn a Quill-HTML blob like "<p><br></p>" into "" so we can
// reject it as empty content.
var stripAllPolicy = bluemonday.StrictPolicy()

// IsSemanticallyEmpty reports whether `s` contains no user text
// once HTML tags and whitespace are removed. Sprint 15a B8 Nit 4:
// the old len(req.Content) < 1 check only caught the completely
// empty string. Quill's "empty" editor submits `<p><br></p>`
// (11 bytes) and the client-side hasContent() catches it, but a
// direct curl to /api/posts would slip past, write an empty post,
// and leave a blank card in the feed. StrictPolicy strips every
// tag leaving only the text nodes; after trimming NBSP/zero-width/
// whitespace, anything still there is real content.
func IsSemanticallyEmpty(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	text := stripAllPolicy.Sanitize(s)
	// bluemonday leaves encoded entities (&nbsp;, &#xA0;, etc) as
	// their literal text. Replace the usual invisible suspects so
	// "<p>&nbsp;</p>" and similar render as empty.
	for _, ws := range []string{"\u00A0", "\u200B", "\u200C", "\u200D", "\uFEFF"} {
		text = strings.ReplaceAll(text, ws, "")
	}
	return strings.TrimSpace(text) == ""
}
