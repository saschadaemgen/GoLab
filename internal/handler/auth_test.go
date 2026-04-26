package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestRegisterRequest_DecodesAllElevenStepsPayload pins the JSON
// shape the Sprint Y.3 brutalist wizard sends to /api/register.
// The wizard splits the eleven application fields across 11 step
// indices (Welcome / Account / 8 fields / Review) but submits a
// single JSON body holding all of them. This test verifies the
// JSON produced by walking the full 11-step flow lands on the
// right registerRequest property and passes validateApplication
// without modification - the schema and validation contract is
// unchanged from Sprint Y.1.
//
// Note: the test name carries forward the original Y.2 test
// (TestRegisterRequest_DecodesWizardPayload) intent but is
// renamed to make the 11-step contract explicit. Same fields,
// same validation, just routed through eleven UI steps instead
// of five.
//
// Reading the assertion side: a future template refactor that
// renames a JSON key (e.g. "ecosystem_connection" -> "ecosystem")
// fails this test loudly before reaching production.
func TestRegisterRequest_DecodesAllElevenStepsPayload(t *testing.T) {
	wizardPayload := []byte(`{
		"username": "applicant",
		"password": "very-secret-12345",
		"external_links": "https://github.com/applicant",
		"ecosystem_connection": "I run a SimpleX SMP relay and read the GoChat protocol design notes weekly.",
		"community_contribution": "Hardware integration write-ups, security review of relay configs.",
		"current_focus": "Cross-compiling SimpleGoX for ARM SBCs.",
		"application_notes": "Available for code review on weekends.",
		"technical_depth_choice": "a",
		"technical_depth_answer": "Double Ratchet's biggest weakness in practice is the post-compromise recovery window: an attacker who briefly captured a chain key sees every following message until the next ratchet step. With high-latency channels this gap matters.",
		"practical_experience": "Yes - small SimpleX SMP relay on a personal SBC.",
		"critical_thinking": "Telegram marketing 'secret chats' as the same product as default cloud chats."
	}`)

	var req registerRequest
	if err := json.Unmarshal(wizardPayload, &req); err != nil {
		t.Fatalf("unmarshal wizard payload: %v", err)
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"username", req.Username, "applicant"},
		{"password", req.Password, "very-secret-12345"},
		{"external_links", req.ExternalLinks, "https://github.com/applicant"},
		{"ecosystem_connection_prefix", req.EcosystemConnection[:21], "I run a SimpleX SMP r"},
		{"community_contribution_prefix", req.CommunityContribution[:20], "Hardware integration"},
		{"current_focus", req.CurrentFocus, "Cross-compiling SimpleGoX for ARM SBCs."},
		{"application_notes", req.ApplicationNotes, "Available for code review on weekends."},
		{"technical_depth_choice", req.TechnicalDepthChoice, "a"},
		{"technical_depth_answer_prefix", req.TechnicalDepthAnswer[:20], "Double Ratchet's big"},
		{"practical_experience_prefix", req.PracticalExperience[:5], "Yes -"},
		{"critical_thinking_prefix", req.CriticalThinking[:8], "Telegram"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}

	// Sanity: the same payload validates cleanly. Catches a future
	// refactor that splits validateApplication's contract from the
	// wizard's intent.
	if err := validateApplication(&req); err != nil {
		t.Errorf("validateApplication on full wizard payload: unexpected error %v", err)
	}
}

// TestValidateApplication locks in the application-gate rules
// without touching the DB or HTTP layer. The handler delegates
// every content check to validateApplication, so these cases pin
// the user-visible behaviour.
//
// Sprint X.1 changes pinned here:
//   - external_links is optional (was required)
//   - ecosystem_connection 30-800 (was 50-500)
//   - community_contribution 30-600 (was 50-400)
//   - current_focus 0-400 (was 0-300)
//   - application_notes 0-300 (was 0-200)
//   - error type is *fieldError so the handler can extract Field
//     for inline UI mapping; tests still match on the Code substring.
//
// Sprint Y.1 additions pinned here:
//   - technical_depth_choice required, must be a / b / c
//   - technical_depth_answer required, 100-500 chars
//   - practical_experience optional, 0-400
//   - critical_thinking optional, 0-400
func TestValidateApplication(t *testing.T) {
	const goodLinks = "https://github.com/example  https://example.dev"
	min30 := strings.Repeat("a", 30)
	max600 := strings.Repeat("a", 600)
	max800 := strings.Repeat("a", 800)
	// Sprint Y.1: 100 is the new minimum for the technical depth
	// answer; 500 the cap. Keep a realistic mid-length sample for
	// the happy-path cases so they read naturally.
	good100 := strings.Repeat("z", 100)
	max500 := strings.Repeat("z", 500)

	// withKnowledge returns a baseline registerRequest pre-populated
	// with valid Sprint Y.1 fields so existing X.1 cases that only
	// care about ecosystem / community / etc do not trip the new
	// gates. Cases that DO test knowledge fields override the
	// relevant subset directly.
	withKnowledge := func(r registerRequest) registerRequest {
		if r.TechnicalDepthChoice == "" {
			r.TechnicalDepthChoice = "a"
		}
		if r.TechnicalDepthAnswer == "" {
			r.TechnicalDepthAnswer = good100
		}
		return r
	}

	cases := []struct {
		name      string
		req       registerRequest
		wantOk    bool
		wantField string
		wantCode  string
	}{
		// ---- Sprint X.1 happy and unhappy paths ----
		{
			name: "complete application accepted",
			req: withKnowledge(registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          "Hardware integration work.",
				ApplicationNotes:      "Available for code review.",
			}),
			wantOk: true,
		},
		{
			name: "complete with optional fields empty",
			req: withKnowledge(registerRequest{
				ExternalLinks:         goodLinks,
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "external_links empty is now accepted (Sprint X.1)",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "external_links present but no valid URL",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "github yourhandle codeberg",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk:    false,
			wantField: "external_links",
			wantCode:  "external_links_invalid",
		},
		{
			name: "external_links http (not https) rejected",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "http://example.com",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk:    false,
			wantField: "external_links",
			wantCode:  "external_links_invalid",
		},
		{
			name: "external_links one valid plus other text accepted",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "see https://example.com or look up Maria",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "external_links comma-separated accepted",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "https://github.com/a,https://example.org",
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "ecosystem_connection at exactly 30 chars accepted",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "ecosystem_connection at 29 chars rejected",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   strings.Repeat("a", 29),
				CommunityContribution: min30,
			}),
			wantOk:    false,
			wantField: "ecosystem_connection",
			wantCode:  "ecosystem_connection_too_short",
		},
		{
			name: "community_contribution too short",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: "code review",
			}),
			wantOk:    false,
			wantField: "community_contribution",
			wantCode:  "community_contribution_too_short",
		},
		{
			name: "ecosystem_connection at 800 chars accepted",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   max800,
				CommunityContribution: min30,
			}),
			wantOk: true,
		},
		{
			name: "ecosystem_connection at 801 chars rejected",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   max800 + "x",
				CommunityContribution: min30,
			}),
			wantOk:    false,
			wantField: "ecosystem_connection",
			wantCode:  "ecosystem_connection_too_long",
		},
		{
			name: "community_contribution at 600 chars accepted",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: max600,
			}),
			wantOk: true,
		},
		{
			name: "community_contribution at 601 chars rejected",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: max600 + "x",
			}),
			wantOk:    false,
			wantField: "community_contribution",
			wantCode:  "community_contribution_too_long",
		},
		{
			name: "current_focus at 400 chars accepted",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          strings.Repeat("a", 400),
			}),
			wantOk: true,
		},
		{
			name: "current_focus at 401 chars rejected",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				CurrentFocus:          strings.Repeat("a", 401),
			}),
			wantOk:    false,
			wantField: "current_focus",
			wantCode:  "current_focus_too_long",
		},
		{
			name: "application_notes at 300 chars accepted",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				ApplicationNotes:      strings.Repeat("a", 300),
			}),
			wantOk: true,
		},
		{
			name: "application_notes at 301 chars rejected",
			req: withKnowledge(registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				ApplicationNotes:      strings.Repeat("a", 301),
			}),
			wantOk:    false,
			wantField: "application_notes",
			wantCode:  "application_notes_too_long",
		},
		{
			name: "whitespace gets trimmed before length check",
			req: withKnowledge(registerRequest{
				ExternalLinks:         "  https://example.com  ",
				EcosystemConnection:   "  " + min30 + "  ",
				CommunityContribution: "  " + min30 + "  ",
			}),
			wantOk: true,
		},

		// ---- Sprint Y.1 knowledge-question cases ----
		{
			name: "accepts technical_depth_choice a",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
			},
			wantOk: true,
		},
		{
			name: "accepts technical_depth_choice b",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "b",
				TechnicalDepthAnswer:  good100,
			},
			wantOk: true,
		},
		{
			name: "accepts technical_depth_choice c",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "c",
				TechnicalDepthAnswer:  good100,
			},
			wantOk: true,
		},
		{
			name: "rejects technical_depth_choice invalid",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "d",
				TechnicalDepthAnswer:  good100,
			},
			wantOk:    false,
			wantField: "technical_depth_choice",
			wantCode:  "technical_depth_choice_invalid",
		},
		{
			name: "rejects technical_depth_choice empty",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "",
				TechnicalDepthAnswer:  good100,
			},
			wantOk:    false,
			wantField: "technical_depth_choice",
			wantCode:  "technical_depth_choice_invalid",
		},
		{
			name: "rejects technical_depth_answer 99 chars (just under min)",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  strings.Repeat("z", 99),
			},
			wantOk:    false,
			wantField: "technical_depth_answer",
			wantCode:  "technical_depth_answer_too_short",
		},
		{
			name: "accepts technical_depth_answer at exactly 100 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
			},
			wantOk: true,
		},
		{
			name: "accepts technical_depth_answer at 500 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "b",
				TechnicalDepthAnswer:  max500,
			},
			wantOk: true,
		},
		{
			name: "rejects technical_depth_answer at 501 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "b",
				TechnicalDepthAnswer:  max500 + "z",
			},
			wantOk:    false,
			wantField: "technical_depth_answer",
			wantCode:  "technical_depth_answer_too_long",
		},
		{
			name: "accepts practical_experience empty",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				PracticalExperience:   "",
			},
			wantOk: true,
		},
		{
			name: "accepts practical_experience at 400 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				PracticalExperience:   strings.Repeat("p", 400),
			},
			wantOk: true,
		},
		{
			name: "rejects practical_experience at 401 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				PracticalExperience:   strings.Repeat("p", 401),
			},
			wantOk:    false,
			wantField: "practical_experience",
			wantCode:  "practical_experience_too_long",
		},
		{
			name: "accepts critical_thinking empty",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				CriticalThinking:      "",
			},
			wantOk: true,
		},
		{
			name: "accepts critical_thinking at 400 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				CriticalThinking:      strings.Repeat("k", 400),
			},
			wantOk: true,
		},
		{
			name: "rejects critical_thinking at 401 chars",
			req: registerRequest{
				EcosystemConnection:   min30,
				CommunityContribution: min30,
				TechnicalDepthChoice:  "a",
				TechnicalDepthAnswer:  good100,
				CriticalThinking:      strings.Repeat("k", 401),
			},
			wantOk:    false,
			wantField: "critical_thinking",
			wantCode:  "critical_thinking_too_long",
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
			if !strings.Contains(err.Error(), c.wantCode) {
				t.Errorf("Error() = %q does not contain code %q", err.Error(), c.wantCode)
			}
		})
	}
}

// TestIsReservedUsername pins the Sprint Y.4 reserved-list. The
// list is a security boundary: a registrant must never be able
// to claim a role-sounding handle that would impersonate the
// platform. Comparison is case-insensitive.
func TestIsReservedUsername(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Reserved (must be true)
		{"admin", true},
		{"Admin", true},   // case-insensitive
		{"ADMIN", true},
		{"root", true},
		{"system", true},
		{"moderator", true},
		{"support", true},
		{"api", true},
		{"www", true},
		{"mail", true},
		{"info", true},
		{"golab", true},
		{"GOLAB", true},
		{"simplego", true},
		{"anonymous", true},
		{"null", true},
		{"undefined", true},
		{"test", true},
		{"dev", true},
		{"staging", true},

		// Free (must be false)
		{"maria", false},
		{"prinz", false},
		{"hardware_hacker", false},
		{"admin1", false},     // not exactly "admin"
		{"administrator", false}, // not exactly "admin"
		{"my_admin", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := isReservedUsername(c.in); got != c.want {
				t.Errorf("isReservedUsername(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestUsernameAvailable_RejectsInvalid pins the format-rejection
// path of CheckUsernameAvailable: handles that fail the regex
// (too short, bad chars, too long) come back with reason
// "invalid". The endpoint MUST NOT 4xx for these - the caller
// expects a 200 OK with a JSON body so the wizard's status
// pill can flip without a fetch error branch.
func TestUsernameAvailable_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name     string
		username string
	}{
		{"too short (2 chars)", "ab"},
		{"empty after trim", "   "},
		{"contains space", "my user"},
		{"contains hyphen", "my-user"},
		{"contains dot", "user.name"},
		{"too long (33 chars)", "abcdefghijklmnopqrstuvwxyz0123456"},
		{"emoji", "user☃"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// url-encode the test username so spaces / unicode do
			// not break NewRequest's URL parsing. The handler
			// reads via r.URL.Query().Get which decodes back.
			req := httptest.NewRequest("GET",
				"/api/auth/username-available?u="+
					url.QueryEscape(c.username), nil)
			rec := httptest.NewRecorder()
			h := &AuthHandler{} // Users not needed for invalid path
			h.CheckUsernameAvailable(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				Available bool   `json:"available"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Available {
				t.Errorf("Available = true, want false")
			}
			// Empty string takes the "empty" path; everything else
			// goes through "invalid".
			if c.username == "   " {
				if body.Reason != "empty" {
					t.Errorf("Reason = %q, want %q", body.Reason, "empty")
				}
			} else if body.Reason != "invalid" {
				t.Errorf("Reason = %q, want %q", body.Reason, "invalid")
			}
		})
	}
}

// TestUsernameAvailable_RejectsReserved pins the reserved-list
// path: an applicant typing "admin" must see reason "reserved",
// not "taken". The DB is not consulted on this path.
func TestUsernameAvailable_RejectsReserved(t *testing.T) {
	cases := []string{"admin", "moderator", "golab", "system", "Root"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET",
				"/api/auth/username-available?u="+name, nil)
			rec := httptest.NewRecorder()
			h := &AuthHandler{} // Users.UsernameExists is never called
			h.CheckUsernameAvailable(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				Available bool   `json:"available"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Available {
				t.Errorf("Available = true, want false")
			}
			if body.Reason != "reserved" {
				t.Errorf("Reason = %q, want %q", body.Reason, "reserved")
			}
		})
	}
}

// TestUsernameAvailable_AcceptsAvailable / RejectsTaken need a
// real DB to exercise the UsernameExists call. They live in the
// integration suite (build-tagged "integration") next to the
// other Sprint X.2 register integration tests, opt-in via:
//
//   GOLAB_TEST_DB=postgres://... go test -tags integration ./internal/handler/...
//
// The tests above cover the validation paths that do not touch
// the DB; the remaining two paths run on demand with a real
// connection.

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
		{"github.com/handle", false},
		{"http://example.com", false},
		{"ftp://example.com", false},
		{"javascript:alert(1)", false},
		{"https://", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := hasValidHTTPSURL(c.in); got != c.want {
				t.Errorf("hasValidHTTPSURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
