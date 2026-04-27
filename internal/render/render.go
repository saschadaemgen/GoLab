package render

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/saschadaemgen/GoLab/internal/model"
)

// Engine renders pages and HTMX fragments.
//
// Pages are full HTML (base.html + partials + page template).
// Fragments are small HTML snippets returned in response to HX-Request
// for partial updates without full page reloads.
type Engine struct {
	pages     map[string]*template.Template
	fragments *template.Template
}

// Top-level page templates that extend base.html.
var pageNames = []string{
	"home",
	"register",
	"login",
	"feed",
	"channel",
	"explore",
	"profile",
	"settings",
	"thread",
	"admin",
	"space",
	"tag",
	"pending",
	// Sprint 16b: Project system pages.
	"project-list",
	"project-show",
	"project-docs",
	"project-doc",
	"project-seasons",
	"project-season",
	"project-members",
}

func New(templatesDir string) (*Engine, error) {
	basePath := filepath.Join(templatesDir, "base.html")
	partialsGlob := filepath.Join(templatesDir, "partials", "*.html")
	partials, err := filepath.Glob(partialsGlob)
	if err != nil {
		return nil, fmt.Errorf("globbing partials: %w", err)
	}

	e := &Engine{pages: make(map[string]*template.Template)}

	// Parse each page with base + all partials
	for _, page := range pageNames {
		pagePath := filepath.Join(templatesDir, page+".html")
		files := []string{basePath}
		files = append(files, partials...)
		files = append(files, pagePath)

		tmpl := template.New("base.html").Funcs(funcMap())
		parsed, err := tmpl.ParseFiles(files...)
		if err != nil {
			return nil, fmt.Errorf("parsing page %s: %w", page, err)
		}
		e.pages[page] = parsed
	}

	// Parse fragments (for HTMX partial responses)
	fragmentsGlob := filepath.Join(templatesDir, "fragments", "*.html")
	fragmentFiles, err := filepath.Glob(fragmentsGlob)
	if err != nil {
		return nil, fmt.Errorf("globbing fragments: %w", err)
	}
	if len(fragmentFiles) > 0 {
		// Include partials so fragments can reference shared components
		all := append([]string{}, partials...)
		all = append(all, fragmentFiles...)
		frag, err := template.New("fragments").Funcs(funcMap()).ParseFiles(all...)
		if err != nil {
			return nil, fmt.Errorf("parsing fragments: %w", err)
		}
		e.fragments = frag
	}

	return e, nil
}

// Render writes a full HTML page to w.
func (e *Engine) Render(w http.ResponseWriter, page string, data any) error {
	tmpl, ok := e.pages[page]
	if !ok {
		return fmt.Errorf("page template not found: %s", page)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, "base.html", data)
}

// RenderContent writes only the "content" block of a page template.
// Use this for HTMX boosted navigation (HX-Request: true) where the
// navbar and footer stay in place and only #main gets swapped.
func (e *Engine) RenderContent(w http.ResponseWriter, page string, data any) error {
	tmpl, ok := e.pages[page]
	if !ok {
		return fmt.Errorf("page template not found: %s", page)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, "content", data)
}

// RenderFragment writes a named fragment template as HTTP response.
// Use for HTMX responses that inject into existing pages
// (new post cards, updated lists, etc.).
func (e *Engine) RenderFragment(w http.ResponseWriter, name string, data any) error {
	if e.fragments == nil {
		return fmt.Errorf("no fragments registered")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return e.fragments.ExecuteTemplate(w, name, data)
}

// RenderFragmentTo writes a named fragment to any io.Writer. Use when
// you need the fragment as a string (e.g. to embed in a WebSocket message).
func (e *Engine) RenderFragmentTo(w io.Writer, name string, data any) error {
	if e.fragments == nil {
		return fmt.Errorf("no fragments registered")
	}
	return e.fragments.ExecuteTemplate(w, name, data)
}

// Auto chooses between full page and content-only based on HX-Request
// header. Most page handlers can just call this.
func (e *Engine) Auto(w http.ResponseWriter, r *http.Request, page string, data any) error {
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") == "true" {
		return e.RenderContent(w, page, data)
	}
	return e.Render(w, page, data)
}

// funcMap returns template helpers for date formatting, string ops, etc.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"timeAgo":   timeAgo,
		"initial":   initial,
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"year":      func() int { return time.Now().Year() },
		"safeHTML":  func(s string) template.HTML { return template.HTML(s) },
		"truncate":  truncate,
		"pluralize": pluralize,
		"isActive":  isActive,
		"dict":      dict,
		// Sprint 14 multi-reaction helpers.
		"emojiFor":      emojiFor,
		"reactionTypes": reactionTypes,
		"contains":      contains,
		// Sprint X application helpers.
		"renderApplicationLinks": renderApplicationLinks,
		// Sprint Y rating widget helpers.
		"ratingDim":     ratingDim,
		"ratingAverage": ratingAverage,
		"ratingCount":   ratingCount,
		"ratingNotesJS": ratingNotesJS,
	}
}

// ratingDim returns the integer value of a named dimension on an
// ApplicationRating, or 0 when the dimension is nil. Templates use
// it to feed the per-field star widget its initial value:
//   {{ template "rating-widget.html" (dict ... "Value" (ratingDim $r "track_record")) }}
//
// The widget interprets 0 as "unrated" and renders no filled stars;
// any 1-10 value renders that many filled stars. Centralising the
// pointer-deref here keeps the template branchless.
func ratingDim(r *model.ApplicationRating, dimension string) int {
	if r == nil {
		return 0
	}
	var v *int
	switch dimension {
	case "track_record":
		v = r.TrackRecord
	case "ecosystem_fit":
		v = r.EcosystemFit
	case "contribution_potential":
		v = r.ContributionPotential
	case "relevance":
		v = r.Relevance
	case "communication":
		v = r.Communication
	}
	if v == nil {
		return 0
	}
	return *v
}

// ratingAverage is a thin template wrapper around
// ApplicationRating.Average so the admin template stays free of
// nil-receiver branches. Returns 0 when r is nil.
func ratingAverage(r *model.ApplicationRating) float64 {
	return r.Average()
}

// ratingCount is the template-side counterpart of
// ApplicationRating.RatedCount. Same nil-handling story.
func ratingCount(r *model.ApplicationRating) int {
	return r.RatedCount()
}

// ratingNotesJS escapes the rating notes string for safe embedding
// as an Alpine factory argument. The notes blob is user-controlled
// (admin types into a textarea) and gets injected into a script-
// expression context: x-data="ratingNotes(<id>, <notes>)". A
// newline or quote in the notes would break the expression and
// could in principle be hijacked. We marshal as JSON so the result
// is always a valid JS string literal with embedded newlines and
// quotes properly escaped.
func ratingNotesJS(r *model.ApplicationRating) template.JS {
	if r == nil {
		return template.JS(`""`)
	}
	b, err := json.Marshal(r.Notes)
	if err != nil {
		return template.JS(`""`)
	}
	return template.JS(b)
}

// renderApplicationLinks turns the user-submitted ExternalLinks blob
// into clickable anchors plus any leftover non-URL text. Used by the
// admin pending-users panel so reviewers can audit applicants' work
// in one click. Splits on whitespace and commas; emits an <a> for
// every token whose URL.Parse succeeds with scheme=https and a
// non-empty host. Tokens that fail validation render as plain text
// (Go's html/template auto-escapes them).
//
// Each link gets target="_blank", rel="noopener noreferrer nofollow":
// - noopener: don't expose this window to the opened tab via
//   window.opener (a basic anti-phishing precaution)
// - noreferrer: don't leak our hostname in the Referer header
// - nofollow: don't grant SEO weight; the link is moderation
//   evidence, not an endorsement
func renderApplicationLinks(blob string) template.HTML {
	if strings.TrimSpace(blob) == "" {
		return ""
	}
	// Split keeping the original separators so the rendered output
	// matches the user's formatting (newline-separated stays on
	// separate lines). We tokenise into "url" / "text" runs and
	// emit each.
	var b strings.Builder
	for _, raw := range strings.Fields(strings.ReplaceAll(blob, ",", " ")) {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		if u, err := url.Parse(token); err == nil && u.Scheme == "https" && u.Host != "" {
			fmt.Fprintf(&b,
				`<a href="%s" target="_blank" rel="noopener noreferrer nofollow">%s</a> `,
				template.HTMLEscapeString(u.String()),
				template.HTMLEscapeString(u.String()),
			)
			continue
		}
		fmt.Fprintf(&b, "%s ", template.HTMLEscapeString(token))
	}
	out := strings.TrimSpace(b.String())
	return template.HTML(out)
}

// emojiFor looks up the glyph for a reaction type. Returns the
// raw type name unchanged if it isn't in the allowlist so mis-
// typed template code doesn't render empty cells.
func emojiFor(t string) string {
	if e, ok := model.ReactionEmoji[t]; ok {
		return e
	}
	return t
}

// reactionTypes returns the canonical display order for the six
// Sprint 14 emoji chips. Exposed to templates so ranging over
// them doesn't hard-code the list in two places.
func reactionTypes() []string {
	return model.ReactionTypesOrdered
}

// contains reports whether needle is in haystack. Used in the
// post-card template to decide whether a reaction chip should
// render in its active (user-pressed) state. Template-friendly:
// accepts nil slices and returns false rather than panicking.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// dict builds a map[string]any from key/value pairs. Used in templates
// to pass multiple named values to a sub-template.
// Example: {{ template "post-card.html" (dict "Post" . "User" $.User) }}
func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict requires an even number of arguments")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2, 2006")
	}
}

func initial(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "?"
	}
	return strings.ToUpper(string([]rune(s)[0]))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// pluralize returns singular/plural based on n. Accepts int or int64
// (Space.PostCount is int64 from the COUNT(*) aggregate) so callers
// do not have to remember to cast. Any other type is treated as
// plural, which is the safe default.
func pluralize(n any, singular, plural string) string {
	switch v := n.(type) {
	case int:
		if v == 1 {
			return singular
		}
	case int64:
		if v == 1 {
			return singular
		}
	case int32:
		if v == 1 {
			return singular
		}
	}
	return plural
}

// isActive returns "current" when current path matches or starts with prefix,
// for navbar link highlighting.
func isActive(current, prefix string) string {
	if current == prefix {
		return "current"
	}
	if prefix != "/" && strings.HasPrefix(current, prefix) {
		return "current"
	}
	return ""
}
