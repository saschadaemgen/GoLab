package handler

import (
	"strings"
	"testing"
)

// TestValidateApplication locks in the Sprint X application gate
// rules without touching the DB or HTTP layer. The handler delegates
// every content check to validateApplication, so these cases pin the
// user-visible behaviour.
//
// Sprint X.1 changes pinned here:
//   - external_links is optional (was required)
//   - ecosystem_connection minimum is 30 (was 50), max is 800
//     (was 500)
//   - community_contribution minimum is 30 (was 50), max is 600
//     (was 400)
//   - current_focus max is 400 (was 300)
//   - application_notes max is 300 (was 200)
//   - error type is *fieldError so the handler can extract Field
//     for inline UI mapping; tests still match on the Code substring.
func TestValidateApplication(t *testing.T) {
	const goodLinks = "https://github.com/example  https://example.dev"
	// Sprint X.1: 30 is the new minimum length for the long-form
	// fields, 600 / 800 the new caps. Build sample strings that
	// land at meaningful boundaries.
	min30 := strings.Repeat("a", 30)
	max600 := strings.Repeat("a", 600)
	max800 := strings.Repeat("a", 800)

	cases := []struct {
		name      string
		req       registerRequest
		wantOk    bool
		wantField string // expected fieldError.Field; empty when wantOk
		wantCode  string // expected fieldError.Code; empty when wantOk
	}{
		{
			name: "complete application accepted",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          "I am working on hardware integration.",
				ApplicationNotes:      "Available for code review.",
			},
			wantOk: true,
		},
		{
			name: "complete with optional fields empty",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "external_links empty is now accepted (Sprint X.1)",
			req: registerRequest{
				ExternalLinks:         "",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "external_links present but no valid URL",
			req: registerRequest{
				ExternalLinks:         "github yourhandle codeberg",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk:    false,
			wantField: "external_links",
			wantCode:  "external_links_invalid",
		},
		{
			name: "external_links http (not https) rejected",
			req: registerRequest{
				ExternalLinks:         "http://example.com",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk:    false,
			wantField: "external_links",
			wantCode:  "external_links_invalid",
		},
		{
			name: "external_links one valid plus other text accepted",
			req: registerRequest{
				ExternalLinks:         "see https://example.com or look up Maria",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "external_links comma-separated accepted",
			req: registerRequest{
				ExternalLinks:         "https://github.com/a,https://example.org",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "ecosystem_connection at exactly 30 chars accepted",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "ecosystem_connection at 29 chars rejected",
			req: registerRequest{
				EcosystemConnection:   strings.Repeat("a", 29),
				CommunityContribution: min30,
			},
			wantOk:    false,
			wantField: "ecosystem_connection",
			wantCode:  "ecosystem_connection_too_short",
		},
		{
			name: "community_contribution too short",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: "code review",
			},
			wantOk:    false,
			wantField: "community_contribution",
			wantCode:  "community_contribution_too_short",
		},
		{
			name: "ecosystem_connection at 800 chars accepted",
			req: registerRequest{
				EcosystemConnection:   max800,
				CommunityContribution: min30,
			},
			wantOk: true,
		},
		{
			name: "ecosystem_connection at 801 chars rejected",
			req: registerRequest{
				EcosystemConnection:   max800 + "x",
				CommunityContribution: min30,
			},
			wantOk:    false,
			wantField: "ecosystem_connection",
			wantCode:  "ecosystem_connection_too_long",
		},
		{
			name: "community_contribution at 600 chars accepted",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: max600,
			},
			wantOk: true,
		},
		{
			name: "community_contribution at 601 chars rejected",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: max600 + "x",
			},
			wantOk:    false,
			wantField: "community_contribution",
			wantCode:  "community_contribution_too_long",
		},
		{
			name: "current_focus at 400 chars accepted",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          strings.Repeat("a", 400),
			},
			wantOk: true,
		},
		{
			name: "current_focus at 401 chars rejected",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          strings.Repeat("a", 401),
			},
			wantOk:    false,
			wantField: "current_focus",
			wantCode:  "current_focus_too_long",
		},
		{
			name: "application_notes at 300 chars accepted",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				ApplicationNotes:      strings.Repeat("a", 300),
			},
			wantOk: true,
		},
		{
			name: "application_notes at 301 chars rejected",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				ApplicationNotes:      strings.Repeat("a", 301),
			},
			wantOk:    false,
			wantField: "application_notes",
			wantCode:  "application_notes_too_long",
		},
		{
			name: "whitespace gets trimmed before length check",
			req: registerRequest{
				ExternalLinks:         "  https://example.com  ",
				EcosystemConnection:   "  " + min30 + "  ",
				CommunityContribution: "  " + min30 + "  ",
			},
			wantOk: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.req
			err := validateApplication(&req)
			if c.wantOk {
				if err != nil {
					t.Errorf("expected ok, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error with code %q, got nil", c.wantCode)
				return
			}
			fe, ok := err.(*fieldError)
			if !ok {
				t.Errorf("expected *fieldError, got %T (%v)", err, err)
				return
			}
			if c.wantField != "" && fe.Field != c.wantField {
				t.Errorf("Field = %q, want %q", fe.Field, c.wantField)
			}
			if c.wantCode != "" && fe.Code != c.wantCode {
				t.Errorf("Code = %q, want %q", fe.Code, c.wantCode)
			}
			// Ensure the rendered error string still contains the code
			// for any caller that joins on Error() instead of using
			// errors.As.
			if !strings.Contains(err.Error(), c.wantCode) {
				t.Errorf("Error() = %q does not contain code %q", err.Error(), c.wantCode)
			}
		})
	}
}

// TestHasValidHTTPSURL pins the helper used by validateApplication
// so its acceptance criteria stay obvious. github / codeberg /
// gitlab / custom domains all pass; non-https schemes and missing
// hosts do not.
func TestHasValidHTTPSURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/handle", true},
		{"https://codeberg.org/handle", true},
		{"https://gitlab.com/handle", true},
		{"https://my-personal-domain.dev", true},
		{"https://onion.address.example", true},
		{"  https://github.com/handle  ", true},
		{"https://a.com,https://b.org", true},
		{"https://a.com https://b.org", true},
		{"please look at https://example.com", true},

		{"", false},
		{"   ", false},
		{"github", false},
		{"github.com/handle", false},   // no scheme
		{"http://example.com", false},  // http not https
		{"ftp://example.com", false},
		{"javascript:alert(1)", false}, // no host
		{"https://", false},            // no host
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := hasValidHTTPSURL(c.in); got != c.want {
				t.Errorf("hasValidHTTPSURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
