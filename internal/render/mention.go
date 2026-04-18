package render

import (
	"regexp"
	"strings"
)

// mentionLinkPattern matches `@username` tokens in already-rendered
// HTML. The lookbehind alternatives (`^` | whitespace | `>` | one
// of `([{,.;!?`) keep the regex from matching inside tag attributes
// (`id="foo@bar"`) and inside email-like sequences (`foo@bar.com`).
// The `\b` on the tail keeps it from matching `@admin_panel` when
// only `@admin` was intended - trailing punctuation or whitespace
// terminates cleanly.
var mentionLinkPattern = regexp.MustCompile(
	`(^|[\s>(\[{,.;!?])@([a-zA-Z0-9_]{3,32})\b`)

// MentionResolver reports whether a username corresponds to an
// existing, active user. The handler provides one via a closure
// so lookups can be memoised per-request. Returning false means
// "leave the raw @username text in place" - never-registered or
// banned names should not become links.
type MentionResolver func(username string) (userID int64, exists bool)

// LinkMentions rewrites every `@username` token in html to an
// `<a href="/u/USERNAME" class="mention">@USERNAME</a>` anchor
// when the resolver confirms the user exists. Non-existent users
// are left untouched so typos don't become mysteriously dead
// links.
//
// The function runs AFTER bluemonday sanitisation - the <a href>
// + class attributes we emit are already in the UGCPolicy allow-
// list, and the href value is built from a regex-validated
// alphanumeric-underscore username so it can't carry an XSS
// payload.
//
// Callers pass a nil resolver as a shortcut for "skip the pass";
// LinkMentions just returns html unchanged.
func LinkMentions(html string, resolve MentionResolver) string {
	if resolve == nil || html == "" {
		return html
	}
	return mentionLinkPattern.ReplaceAllStringFunc(html, func(match string) string {
		// Split match into its leading boundary char + @ + username.
		// FindStringSubmatchIndex would be cleaner, but we can't call
		// that from inside ReplaceAllStringFunc without re-running
		// the regex; slicing by "@" is unambiguous here because the
		// username's character class excludes `@`.
		at := strings.Index(match, "@")
		if at < 0 {
			return match
		}
		leading := match[:at]
		username := match[at+1:]
		// Strip trailing punctuation that \b let through (e.g. a
		// comma right after the name). The regex design means this
		// shouldn't fire, but be defensive.
		username = strings.TrimRight(username, ",.;!?)]}")
		if username == "" {
			return match
		}
		if _, ok := resolve(strings.ToLower(username)); !ok {
			return match
		}
		return leading + `<a href="/u/` + username + `" class="mention">@` + username + `</a>`
	})
}
