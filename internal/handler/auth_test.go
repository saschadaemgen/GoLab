package handler

import (
	"strings"
	"testing"
)

// TestValidateApplication locks in the Sprint X application gate
// rules without touching the DB or HTTP layer. The handler delegates
// every content check to validateApplication, so these cases pin the
// user-visible behaviour: the registration form rejects empty,
// malformed, or under-length submissions before any uniqueness check
// runs, and accepts a complete and well-formed application.
func TestValidateApplication(t *testing.T) {
	const goodLinks = "https://github.com/example  https://example.dev"
	// Use strings.Repeat so the bounds are obvious in the test
	// source instead of opaque hand-rolled strings.
	good50 := strings.Repeat("a", 50)
	good400 := strings.Repeat("a", 400)
	good500 := strings.Repeat("a", 500)

	cases := []struct {
		name      string
		req       registerRequest
		wantOk    bool
		wantErrIn string // substring; empty means don't care
	}{
		{
			name: "complete application accepted",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: good50,
				CurrentFocus:          "I am working on hardware integration.",
				ApplicationNotes:      "Available for code review.",
			},
			wantOk: true,
		},
		{
			name: "complete with optional fields empty",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: good50,
				// CurrentFocus + ApplicationNotes deliberately empty
			},
			wantOk: true,
		},
		{
			name: "external_links missing",
			req: registerRequest{
				ExternalLinks:         "",
				EcosystemConnection:   good50,
				CommunityContribution: good50,
			},
			wantOk:    false,
			wantErrIn: "external_links_missing",
		},
		{
			name: "external_links present but no valid URL",
			req: registerRequest{
				ExternalLinks:         "github yourhandle codeberg",
				EcosystemConnection:   good50,
				CommunityContribution: good50,
			},
			wantOk:    false,
			wantErrIn: "external_links_invalid",
		},
		{
			name: "external_links http (not https) rejected",
			req: registerRequest{
				ExternalLinks:         "http://example.com",
				EcosystemConnection:   good50,
				CommunityContribution: good50,
			},
			wantOk:    false,
			wantErrIn: "external_links_invalid",
		},
		{
			name: "external_links one valid plus other text accepted",
			req: registerRequest{
				ExternalLinks:         "see https://example.com or look up Maria",
				EcosystemConnection:   good50,
				CommunityContribution: good50,
			},
			wantOk: true,
		},
		{
			name: "external_links comma-separated accepted",
			req: registerRequest{
				ExternalLinks:         "https://github.com/a,https://example.org",
				EcosystemConnection:   good50,
				CommunityContribution: good50,
			},
			wantOk: true,
		},
		{
			name: "ecosystem_connection too short",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   "I use SimpleGo.",
				CommunityContribution: good50,
			},
			wantOk:    false,
			wantErrIn: "ecosystem_connection_too_short",
		},
		{
			name: "community_contribution too short",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: "code review",
			},
			wantOk:    false,
			wantErrIn: "community_contribution_too_short",
		},
		{
			name: "ecosystem_connection too long",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good500 + "x", // 501 chars
				CommunityContribution: good50,
			},
			wantOk:    false,
			wantErrIn: "ecosystem_connection_too_long",
		},
		{
			name: "community_contribution too long",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: good400 + "x", // 401 chars
			},
			wantOk:    false,
			wantErrIn: "community_contribution_too_long",
		},
		{
			name: "current_focus too long",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: good50,
				CurrentFocus:          strings.Repeat("a", 301),
			},
			wantOk:    false,
			wantErrIn: "current_focus_too_long",
		},
		{
			name: "application_notes too long",
			req: registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   good50,
				CommunityContribution: good50,
				ApplicationNotes:      strings.Repeat("a", 201),
			},
			wantOk:    false,
			wantErrIn: "application_notes_too_long",
		},
		{
			name: "whitespace gets trimmed before length check",
			req: registerRequest{
				ExternalLinks:         "  https://example.com  ",
				EcosystemConnection:   "  " + good50 + "  ",
				CommunityContribution: "  " + good50 + "  ",
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
				t.Errorf("expected error containing %q, got nil", c.wantErrIn)
				return
			}
			if c.wantErrIn != "" && !strings.Contains(err.Error(), c.wantErrIn) {
				t.Errorf("expected error containing %q, got %q", c.wantErrIn, err.Error())
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
