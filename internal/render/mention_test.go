package render

import (
	"strings"
	"testing"
)

// TestLinkMentions exercises the Sprint 14 HTML post-processor.
// Resolver behaviour is the load-bearing part: existing users
// become links, unknown users stay as plain text.
func TestLinkMentions(t *testing.T) {
	known := map[string]int64{
		"sascha": 1,
		"maria":  2,
		"peter":  3,
	}
	resolve := func(name string) (int64, bool) {
		id, ok := known[strings.ToLower(name)]
		return id, ok
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "existing user becomes a link",
			in:   "<p>hey @sascha</p>",
			want: `<p>hey <a href="/u/sascha" class="mention">@sascha</a></p>`,
		},
		{
			name: "unknown user stays as plain text",
			in:   "<p>hi @ghost</p>",
			want: "<p>hi @ghost</p>",
		},
		{
			name: "mix of known and unknown",
			in:   "<p>@sascha met @ghost</p>",
			want: `<p><a href="/u/sascha" class="mention">@sascha</a> met @ghost</p>`,
		},
		{
			name: "email addresses untouched",
			in:   `<p>mail foo@example.com</p>`,
			want: `<p>mail foo@example.com</p>`,
		},
		{
			name: "multiple known mentions in one paragraph",
			in:   "<p>@sascha and @maria and @peter</p>",
			want: `<p><a href="/u/sascha" class="mention">@sascha</a> and <a href="/u/maria" class="mention">@maria</a> and <a href="/u/peter" class="mention">@peter</a></p>`,
		},
		{
			name: "nil resolver is a no-op",
			in:   "@sascha",
			want: "@sascha",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			if tc.name == "nil resolver is a no-op" {
				got = LinkMentions(tc.in, nil)
			} else {
				got = LinkMentions(tc.in, resolve)
			}
			if got != tc.want {
				t.Errorf("LinkMentions(%q) =\n  %q\nwant\n  %q", tc.in, got, tc.want)
			}
		})
	}
}
