package render

import "testing"

// TestIsSemanticallyEmpty locks in the Sprint 15a B8 Nit 4 contract:
// content is "empty" when stripping every tag leaves no visible text.
func TestIsSemanticallyEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain empty string", "", true},
		{"only whitespace", "   \t\n  ", true},
		{"quill empty editor", "<p><br></p>", true},
		{"nested empty paragraphs", "<p><br></p><p><br></p>", true},
		{"empty div", "<div></div>", true},
		{"nbsp only", "<p>\u00A0</p>", true},
		{"entity nbsp literal", "<p>&nbsp;</p>", true},
		{"zero-width space", "<p>\u200B</p>", true},
		{"whitespace-only text node", "<p>   </p>", true},

		{"real content", "<p>Hello</p>", false},
		{"one letter", "x", false},
		{"markdown-style heading", "# Hello", false},
		{"mixed tag + text", "<p>hi<br></p>", false},
		// Image with no text content is still "something" a user
		// submitted on purpose (pasting a GIF URL). StrictPolicy
		// strips the tag entirely, so it ends up empty - accept
		// that edge case for now; the client-side hasContent
		// gate takes care of preventing stray image-only posts
		// through the normal flow and a direct curl with just an
		// img would need its own media-presence check elsewhere.
		// This test case documents the current behaviour.
		{"image only (documented gap)", `<p><img src="/x.gif"></p>`, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsSemanticallyEmpty(c.in)
			if got != c.want {
				t.Errorf("IsSemanticallyEmpty(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
