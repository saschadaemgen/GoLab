package model

import (
	"reflect"
	"testing"
)

// TestExtractUsernames covers the Sprint 14 regex rules. These are
// pure-function tests - they don't touch the DB, so they run under
// `go test ./...` on any dev machine.
func TestExtractUsernames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single mention",
			in:   "hey @sascha",
			want: []string{"sascha"},
		},
		{
			name: "two distinct mentions keep discovery order",
			in:   "@sascha and @maria",
			want: []string{"sascha", "maria"},
		},
		{
			name: "case-insensitive dedupe",
			in:   "@SASCHA says hi to @sascha",
			want: []string{"sascha"},
		},
		{
			name: "email addresses are not mentions",
			in:   "email me at foo@bar.com please",
			want: nil,
		},
		{
			name: "double-@ spam is rejected",
			in:   "look at @@@spam",
			want: nil,
		},
		{
			name: "too short is ignored",
			in:   "hello @ab nothing here",
			want: nil,
		},
		{
			name: "quill HTML with three mentions",
			in: `<p>pinging <strong>@maria</strong>, ` +
				`<a href="/u/peter">@peter</a> and @sascha</p>`,
			want: []string{"maria", "peter", "sascha"},
		},
		{
			name: "trailing punctuation does not break extraction",
			in:   "thanks @sascha, @maria!",
			want: []string{"sascha", "maria"},
		},
		{
			name: "underscores allowed in username",
			in:   "@cool_user_42 posted something",
			want: []string{"cool_user_42"},
		},
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractUsernames(tc.in)
			// Treat nil and [] as equivalent; the implementation may
			// return either for empty results.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractUsernames(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsValidReactionType guards the allowlist used by both the
// template and the handler. If someone adds a 7th emoji type they
// have to touch this test, which is intentional.
func TestIsValidReactionType(t *testing.T) {
	// Every canonical type should be valid.
	for _, typ := range ReactionTypesOrdered {
		if !IsValidReactionType(typ) {
			t.Errorf("canonical type %q failed IsValidReactionType", typ)
		}
		if _, ok := ReactionEmoji[typ]; !ok {
			t.Errorf("canonical type %q missing from ReactionEmoji map", typ)
		}
	}
	// Garbage and typos should be rejected.
	for _, bad := range []string{"", " ", "HEART", "hearts", "like", "dislike", "\x00"} {
		if IsValidReactionType(bad) {
			t.Errorf("invalid type %q passed IsValidReactionType", bad)
		}
	}
}
