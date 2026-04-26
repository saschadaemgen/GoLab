package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

var (
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

type AuthHandler struct {
	Users         *model.UserStore
	Sessions      *auth.SessionStore
	Settings      *model.SettingsStore
	Notifications *model.NotificationStore
	Hub           *Hub // optional, used to push notification badges
	Secure        bool // true in production (HTTPS)
}

// registerRequest carries the application-form fields. Sprint X
// removed the email field and added five application fields the
// admin reviews before flipping the user from pending to active.
// Sprint Y.1 added the four knowledge-question fields.
type registerRequest struct {
	Username              string `json:"username"`
	Password              string `json:"password"`
	ExternalLinks         string `json:"external_links"`
	EcosystemConnection   string `json:"ecosystem_connection"`
	CommunityContribution string `json:"community_contribution"`
	CurrentFocus          string `json:"current_focus"`
	ApplicationNotes      string `json:"application_notes"`
	// Sprint Y.1 knowledge questions
	TechnicalDepthChoice string `json:"technical_depth_choice"`
	TechnicalDepthAnswer string `json:"technical_depth_answer"`
	PracticalExperience  string `json:"practical_experience"`
	CriticalThinking     string `json:"critical_thinking"`
}

// loginRequest carries the credentials. Sprint X switched login from
// email-based to username-based to match the new registration flow.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// wantsFormResponse reports whether the caller submitted an HTML form
// (application/x-www-form-urlencoded or multipart/form-data). Those
// callers get an HTTP 303 redirect on success so the URL bar ends up
// clean. AJAX/JSON callers get JSON.
func wantsFormResponse(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(ct, "multipart/form-data")
}

// decodeRegister reads a registration request from either JSON or
// form-encoded body. Returns the parsed request or an error.
func decodeRegister(r *http.Request) (registerRequest, error) {
	var req registerRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.Username = r.Form.Get("username")
		req.Password = r.Form.Get("password")
		req.ExternalLinks = r.Form.Get("external_links")
		req.EcosystemConnection = r.Form.Get("ecosystem_connection")
		req.CommunityContribution = r.Form.Get("community_contribution")
		req.CurrentFocus = r.Form.Get("current_focus")
		req.ApplicationNotes = r.Form.Get("application_notes")
		// Sprint Y.1 knowledge questions
		req.TechnicalDepthChoice = r.Form.Get("technical_depth_choice")
		req.TechnicalDepthAnswer = r.Form.Get("technical_depth_answer")
		req.PracticalExperience = r.Form.Get("practical_experience")
		req.CriticalThinking = r.Form.Get("critical_thinking")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// decodeLogin reads a login request from either JSON or form-encoded body.
func decodeLogin(r *http.Request) (loginRequest, error) {
	var req loginRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.Username = r.Form.Get("username")
		req.Password = r.Form.Get("password")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// redirectOrJSON handles the success path based on how the client submitted.
// Form submissions get a 303 See Other to `path` so the browser does a fresh
// GET and the URL bar loses the POST context. AJAX callers get `jsonBody`.
func redirectOrJSON(w http.ResponseWriter, r *http.Request, path string, jsonBody any) {
	if wantsFormResponse(r) {
		http.Redirect(w, r, path, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, jsonBody)
}

// errorRedirectOrJSON reports an error either as JSON (AJAX) or as a
// redirect back to errorPath with ?error=... (form fallback). Keeps the
// password out of the URL even in the error path.
func errorRedirectOrJSON(w http.ResponseWriter, r *http.Request, errorPath string, status int, message string) {
	if wantsFormResponse(r) {
		http.Redirect(w, r, errorPath+"?error="+urlEncode(message), http.StatusSeeOther)
		return
	}
	writeError(w, status, message)
}

func urlEncode(s string) string {
	// Minimal URL-encode: only encode the characters that break a query string.
	// net/url would be fuller but we want to avoid adding an import path here
	// just for this helper.
	r := strings.NewReplacer(
		"%", "%25",
		"&", "%26",
		"?", "%3F",
		" ", "+",
		"\n", "%0A",
	)
	return r.Replace(s)
}

// applicationFieldLimits defines the length gates for each application
// question. Centralised so the handler, the validation helper, the
// register template counter and the test suite stay in sync.
//
// Sprint X.1 softened the lower bounds on the long-form fields after
// applicants reported the originals felt cramped, and dropped the
// external_links requirement altogether (the field is now optional).
const (
	externalLinksMax         = 500 // optional, no min
	ecosystemConnectionMin   = 30  // was 50
	ecosystemConnectionMax   = 800 // was 500
	communityContributionMin = 30  // was 50
	communityContributionMax = 600 // was 400
	currentFocusMax          = 400 // was 300
	applicationNotesMax      = 300 // was 200
	// Sprint Y.1 knowledge questions
	technicalDepthAnswerMin = 100 // required
	technicalDepthAnswerMax = 500
	practicalExperienceMax  = 400 // optional
	criticalThinkingMax     = 400 // optional
)

// validTechnicalDepthChoices is the allow-list for the choice
// picker on /register. Empty string is rejected by the handler;
// it shows up in the DB only on legacy users from before Sprint
// Y.1 (per migration 028 default).
var validTechnicalDepthChoices = map[string]bool{
	"a": true, "b": true, "c": true,
}

// fieldError carries a structured validation failure. Field is the
// JSON / form-input key the message belongs to ("ecosystem_connection",
// "external_links", etc) so the client can highlight the offending
// textarea inline. Code is the stable machine-readable reason
// ("ecosystem_connection_too_short") tests pin against. Message is
// the human string surfaced in the UI.
//
// Sprint X.1: the registration form switched from "reload page on
// 400" to "fetch + render error inline". Without the structured
// field name the client could not know which textarea to highlight,
// so previously every error appeared at the top and lost its
// connection to the input that caused it.
type fieldError struct {
	Field   string
	Code    string
	Message string
}

func (e *fieldError) Error() string {
	return e.Code + ": " + e.Message
}

// validateApplication runs the Sprint X content rules over a
// registration request and returns the first user-visible error or
// nil when everything passes. Pure function: no DB, no HTTP, easy
// to unit-test directly without standing up the full Register
// handler.
//
// Mutates the request in place to apply consistent whitespace
// trimming so the handler downstream stores the cleaned version.
//
// Sprint X.1: external_links is now optional. If supplied, it must
// still parse to at least one valid https URL - we keep that check
// to reject "asdf" and similar pure-text submissions while letting
// applicants who do not have public links yet apply anyway.
func validateApplication(req *registerRequest) error {
	req.ExternalLinks = strings.TrimSpace(req.ExternalLinks)
	req.EcosystemConnection = strings.TrimSpace(req.EcosystemConnection)
	req.CommunityContribution = strings.TrimSpace(req.CommunityContribution)
	req.CurrentFocus = strings.TrimSpace(req.CurrentFocus)
	req.ApplicationNotes = strings.TrimSpace(req.ApplicationNotes)

	// External links: optional. Only validate format when non-empty.
	if req.ExternalLinks != "" {
		if len(req.ExternalLinks) > externalLinksMax {
			return &fieldError{
				Field:   "external_links",
				Code:    "external_links_too_long",
				Message: fmt.Sprintf("at most %d characters", externalLinksMax),
			}
		}
		if !hasValidHTTPSURL(req.ExternalLinks) {
			return &fieldError{
				Field:   "external_links",
				Code:    "external_links_invalid",
				Message: "please include at least one full https:// URL or leave the field blank",
			}
		}
	}

	// Ecosystem connection: required, length-bounded.
	if len(req.EcosystemConnection) < ecosystemConnectionMin {
		return &fieldError{
			Field:   "ecosystem_connection",
			Code:    "ecosystem_connection_too_short",
			Message: fmt.Sprintf("at least %d characters", ecosystemConnectionMin),
		}
	}
	if len(req.EcosystemConnection) > ecosystemConnectionMax {
		return &fieldError{
			Field:   "ecosystem_connection",
			Code:    "ecosystem_connection_too_long",
			Message: fmt.Sprintf("at most %d characters", ecosystemConnectionMax),
		}
	}

	// Community contribution: required, length-bounded.
	if len(req.CommunityContribution) < communityContributionMin {
		return &fieldError{
			Field:   "community_contribution",
			Code:    "community_contribution_too_short",
			Message: fmt.Sprintf("at least %d characters", communityContributionMin),
		}
	}
	if len(req.CommunityContribution) > communityContributionMax {
		return &fieldError{
			Field:   "community_contribution",
			Code:    "community_contribution_too_long",
			Message: fmt.Sprintf("at most %d characters", communityContributionMax),
		}
	}

	// Optional fields: only enforce upper bound.
	if len(req.CurrentFocus) > currentFocusMax {
		return &fieldError{
			Field:   "current_focus",
			Code:    "current_focus_too_long",
			Message: fmt.Sprintf("at most %d characters", currentFocusMax),
		}
	}
	if len(req.ApplicationNotes) > applicationNotesMax {
		return &fieldError{
			Field:   "application_notes",
			Code:    "application_notes_too_long",
			Message: fmt.Sprintf("at most %d characters", applicationNotesMax),
		}
	}

	// Sprint Y.1 knowledge questions. Trim first so leading /
	// trailing whitespace does not confuse the length gate.
	req.TechnicalDepthChoice = strings.TrimSpace(req.TechnicalDepthChoice)
	req.TechnicalDepthAnswer = strings.TrimSpace(req.TechnicalDepthAnswer)
	req.PracticalExperience = strings.TrimSpace(req.PracticalExperience)
	req.CriticalThinking = strings.TrimSpace(req.CriticalThinking)

	// Technical depth: choice required (a / b / c), answer 100-500.
	if !validTechnicalDepthChoices[req.TechnicalDepthChoice] {
		return &fieldError{
			Field:   "technical_depth_choice",
			Code:    "technical_depth_choice_invalid",
			Message: "please pick one of the three sub-questions (a, b, or c)",
		}
	}
	if len(req.TechnicalDepthAnswer) < technicalDepthAnswerMin {
		return &fieldError{
			Field:   "technical_depth_answer",
			Code:    "technical_depth_answer_too_short",
			Message: fmt.Sprintf("at least %d characters", technicalDepthAnswerMin),
		}
	}
	if len(req.TechnicalDepthAnswer) > technicalDepthAnswerMax {
		return &fieldError{
			Field:   "technical_depth_answer",
			Code:    "technical_depth_answer_too_long",
			Message: fmt.Sprintf("at most %d characters", technicalDepthAnswerMax),
		}
	}

	// Practical experience and critical thinking: optional, max only.
	// Honest "no" answers are accepted; we deliberately do NOT enforce
	// a minimum so an applicant who has not run this stack yet can
	// say so without padding.
	if len(req.PracticalExperience) > practicalExperienceMax {
		return &fieldError{
			Field:   "practical_experience",
			Code:    "practical_experience_too_long",
			Message: fmt.Sprintf("at most %d characters", practicalExperienceMax),
		}
	}
	if len(req.CriticalThinking) > criticalThinkingMax {
		return &fieldError{
			Field:   "critical_thinking",
			Code:    "critical_thinking_too_long",
			Message: fmt.Sprintf("at most %d characters", criticalThinkingMax),
		}
	}
	return nil
}

// hasValidHTTPSURL splits the input on whitespace and commas and
// reports whether at least one resulting token is a syntactically
// valid https URL. We deliberately accept any host (github.com,
// codeberg.org, gitlab.com, personal sites, .onion) - the admin
// reviews the actual content before approving.
func hasValidHTTPSURL(s string) bool {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	})
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		u, err := url.Parse(f)
		if err != nil {
			continue
		}
		if u.Scheme != "https" {
			continue
		}
		if u.Host == "" {
			continue
		}
		return true
	}
	return false
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRegister(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)

	// Validate username
	if !usernameRegex.MatchString(req.Username) {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, "username must be 3-32 alphanumeric characters or underscores")
		return
	}

	// Validate password per NIST SP 800-63B: length only, 8-128 chars.
	if err := validatePassword(req.Password); err != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, err.Error())
		return
	}

	// Sprint X: validate the application content before doing any DB
	// work. This rejects empty / too-short / malformed submissions
	// without burning a username uniqueness check or a bcrypt hash.
	//
	// Sprint X.1: when the call comes from the JSON-driven Alpine
	// form, return the structured field info so the client can
	// pin the inline error to the offending textarea instead of
	// printing a generic top-of-form banner.
	if err := validateApplication(&req); err != nil {
		var fe *fieldError
		if !wantsFormResponse(r) && errors.As(err, &fe) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"field":   fe.Field,
				"code":    fe.Code,
				"message": fe.Message,
			})
			return
		}
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, err.Error())
		return
	}

	// Check username uniqueness
	existing, err := h.Users.FindByUsername(r.Context(), req.Username)
	if err != nil {
		slog.Error("register: find by username", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusConflict, "username already taken")
		return
	}

	// Hash password
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("register: hash password", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}

	// Determine initial status from the platform setting. First user
	// (id will be 1) always ends up active regardless - Store.Create
	// enforces that invariant so the platform isn't DOA behind its
	// own approval gate.
	status := model.UserStatusActive
	if h.Settings != nil && h.Settings.GetBool(r.Context(), "require_approval") {
		status = model.UserStatusPending
	}

	user, err := h.Users.Create(r.Context(), model.UserCreateParams{
		Username:              req.Username,
		PasswordHash:          hash,
		Status:                status,
		ExternalLinks:         req.ExternalLinks,
		EcosystemConnection:   req.EcosystemConnection,
		CommunityContribution: req.CommunityContribution,
		CurrentFocus:          req.CurrentFocus,
		ApplicationNotes:      req.ApplicationNotes,
		TechnicalDepthChoice:  req.TechnicalDepthChoice,
		TechnicalDepthAnswer:  req.TechnicalDepthAnswer,
		PracticalExperience:   req.PracticalExperience,
		CriticalThinking:      req.CriticalThinking,
	})
	if err != nil {
		slog.Error("register: create user", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}

	// Sprint X.2: handler-level first-user promote. The model's
	// Create() already short-circuits to power_level=100 + status
	// active when COUNT(users) reads 0 right before the INSERT, but
	// Der Prinz reported on a fresh DB the first registrant landed
	// at power_level=10 anyway, locking themselves out of /admin.
	// Possible causes include migration 012 leaving zombie rows,
	// transaction-snapshot weirdness on the COUNT, or any future
	// refactor of Create that drops the bootstrap branch silently.
	// Re-applying the promote AFTER the user is in the DB removes
	// the dependency on Create's COUNT branch firing correctly:
	// id == 1 is observable post-INSERT and the UPDATE is idempotent.
	//
	// Auto-approve as well: require_approval is forced on by
	// migration 026 so the first user would otherwise be stuck
	// on /pending with nobody to approve them. Failures are logged
	// but do not abort registration; the user's row is already
	// committed and the worst case is a follow-up manual fix.
	if user.ID == 1 {
		if err := h.Users.Promote(r.Context(), user.ID, 100); err != nil {
			slog.Error("register: promote first user", "error", err)
		} else {
			user.PowerLevel = 100
		}
		if err := h.Users.SetStatus(r.Context(), user.ID, model.UserStatusActive, user.ID); err != nil {
			slog.Error("register: auto-approve first user", "error", err)
		} else {
			user.Status = model.UserStatusActive
		}
	}

	// Session is created either way - pending users can log in and see
	// a read-only view of the platform until admins approve them.
	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("register: create session", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}
	h.setSessionCookie(w, sessionID)

	slog.Info("user registered", "username", user.Username, "id", user.ID, "status", user.Status)

	// If pending, notify every admin so they can review the queue.
	if user.Status == model.UserStatusPending {
		h.notifyAdminsOfNewUser(r.Context(), user)
		redirectOrJSON(w, r, "/pending", map[string]any{"user": user, "status": "pending"})
		return
	}

	redirectOrJSON(w, r, "/feed", map[string]any{"user": user})
}

// notifyAdminsOfNewUser fans out a "new_user" notification (plus a
// WebSocket count-push when a hub is available) so admins see the
// pending badge grow in real time.
func (h *AuthHandler) notifyAdminsOfNewUser(ctx context.Context, newUser *model.User) {
	if h.Users == nil || h.Notifications == nil {
		return
	}
	admins, err := h.Users.ListAdmins(ctx)
	if err != nil {
		slog.Error("register: list admins", "error", err)
		return
	}
	for _, admin := range admins {
		if admin.ID == newUser.ID {
			continue // skip self (first user case, though they're active anyway)
		}
		if _, err := h.Notifications.Create(ctx, admin.ID, newUser.ID, model.NotifNewUser, nil); err != nil {
			slog.Warn("register: create admin notif", "admin", admin.ID, "error", err)
			continue
		}
		if h.Hub != nil {
			count, _ := h.Notifications.UnreadCount(ctx, admin.ID)
			h.Hub.PublishToUser(admin.ID, Message{
				Type: "notification_count",
				Data: map[string]int{"count": count},
			})
		}
	}
}

// validatePassword enforces the NIST SP 800-63B recommendation of length
// only, no composition rules. Minimum 8, maximum 128 characters.
func validatePassword(pw string) error {
	if len(pw) < 8 {
		return errPasswordTooShort
	}
	if len(pw) > 128 {
		return errPasswordTooLong
	}
	return nil
}

var (
	errPasswordTooShort = &passwordError{"password must be at least 8 characters"}
	errPasswordTooLong  = &passwordError{"password must not exceed 128 characters"}
)

type passwordError struct{ msg string }

func (e *passwordError) Error() string { return e.msg }

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	req, err := decodeLogin(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/login", http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		errorRedirectOrJSON(w, r, "/login", http.StatusUnauthorized, "invalid username or password")
		return
	}

	user, err := h.Users.FindByUsername(r.Context(), req.Username)
	if err != nil {
		slog.Error("login: find by username", "error", err)
		errorRedirectOrJSON(w, r, "/login", http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		errorRedirectOrJSON(w, r, "/login", http.StatusUnauthorized, "invalid username or password")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		errorRedirectOrJSON(w, r, "/login", http.StatusUnauthorized, "invalid username or password")
		return
	}

	if user.Banned {
		errorRedirectOrJSON(w, r, "/login", http.StatusForbidden, "this account has been banned")
		return
	}

	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("login: create session", "error", err)
		errorRedirectOrJSON(w, r, "/login", http.StatusInternalServerError, "internal error")
		return
	}

	h.setSessionCookie(w, sessionID)

	slog.Info("user logged in", "username", user.Username, "id", user.ID)
	redirectOrJSON(w, r, "/feed", map[string]any{
		"user": user,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Look up under either cookie name so logging out works right after a
	// deploy switches the name (e.g. from session_id to __Host-golab_session).
	var value string
	for _, name := range []string{"__Host-golab_session", "session_id"} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			value = c.Value
			break
		}
	}
	if value != "" {
		if err := h.Sessions.Delete(r.Context(), value); err != nil {
			slog.Error("logout: delete session", "error", err)
		}
	}

	// Clear both possible cookie names defensively.
	for _, name := range []string{"__Host-golab_session", "session_id"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.Secure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// sessionCookieName returns the appropriate cookie name for the current
// deployment. In production (HTTPS) we use the "__Host-" prefix which the
// browser enforces: only accepted from HTTPS, must have Path=/, must not
// have a Domain attribute. This prevents subdomain cookie-injection
// attacks. In local dev we can't use it because we run plain HTTP.
func (h *AuthHandler) sessionCookieName() string {
	if h.Secure {
		return "__Host-golab_session"
	}
	return "session_id"
}

func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.sessionCookieName(),
		Value:    sessionID,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		Secure:   h.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
}
