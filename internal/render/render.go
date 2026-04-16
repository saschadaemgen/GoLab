package render

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
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
	}
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

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
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
