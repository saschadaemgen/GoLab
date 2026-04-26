package model

import (
	"reflect"
	"testing"
)

// TestUser_NoEmailField guards against accidentally re-introducing
// the email field on the User struct. Sprint X removed email
// permanently (column dropped in migration 026), and several
// downstream layers rely on its absence: the registration and
// login forms have no email input, the admin pending-users panel
// renders application content instead, the profile API no longer
// scrubs an email field on read.
//
// Reflection-based check rather than a compile-time guard because a
// type assertion `var _ struct{ Email string } = User{}` would only
// catch the wrong direction (that User HAS Email). We want the
// assertion: User does NOT have Email.
func TestUser_NoEmailField(t *testing.T) {
	tt := reflect.TypeOf(User{})
	if _, ok := tt.FieldByName("Email"); ok {
		t.Fatal("User has an Email field; Sprint X removed it permanently")
	}
}

// TestUser_HasApplicationFields complements the above: the five
// Sprint X application fields must exist on the User struct so the
// admin pending-users panel can read them.
func TestUser_HasApplicationFields(t *testing.T) {
	tt := reflect.TypeOf(User{})
	required := []string{
		"ExternalLinks",
		"EcosystemConnection",
		"CommunityContribution",
		"CurrentFocus",
		"ApplicationNotes",
	}
	for _, name := range required {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("User is missing the %s field added in Sprint X", name)
			continue
		}
		if f.Type.Kind() != reflect.String {
			t.Errorf("User.%s should be string, got %s", name, f.Type)
		}
	}
}

// TestUser_HasKnowledgeFields locks in the four Sprint Y.1 fields.
// Same shape as the application-fields guard above; ensures a future
// refactor that moves these to a separate table also updates the
// downstream stripping in profile.go / pages.go.
func TestUser_HasKnowledgeFields(t *testing.T) {
	tt := reflect.TypeOf(User{})
	required := []string{
		"TechnicalDepthChoice",
		"TechnicalDepthAnswer",
		"PracticalExperience",
		"CriticalThinking",
	}
	for _, name := range required {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("User is missing the %s field added in Sprint Y.1", name)
			continue
		}
		if f.Type.Kind() != reflect.String {
			t.Errorf("User.%s should be string, got %s", name, f.Type)
		}
	}
}

// TestUserStore_HasPromoteMethod guards the Sprint X.2 Promote
// method's existence and signature. The auth handler's first-user
// auto-promote path calls h.Users.Promote(ctx, id, level) without
// going through the model.UserCreateParams shape; if a future
// refactor accidentally drops the method or changes its signature,
// the handler would still compile until exercise but the test
// catches it before the binary boots.
func TestUserStore_HasPromoteMethod(t *testing.T) {
	tt := reflect.TypeOf(&UserStore{})
	m, ok := tt.MethodByName("Promote")
	if !ok {
		t.Fatal("UserStore is missing the Promote method added in Sprint X.2")
	}
	// Expected signature: (ctx, userID int64, level int) error
	// reflect.Method's Type includes the receiver as arg 0.
	ft := m.Type
	if ft.NumIn() != 4 {
		t.Errorf("Promote takes %d args, want 4 (recv + ctx + id + level)", ft.NumIn())
	}
	if ft.NumOut() != 1 {
		t.Errorf("Promote returns %d values, want 1 (error)", ft.NumOut())
	}
}

// TestUser_StatusConstants is a sanity check that the moderation
// constants stay matched to the values stored in the DB (lowercase
// strings the migration 021 INSERT seeds).
func TestUser_StatusConstants(t *testing.T) {
	cases := map[string]string{
		UserStatusActive:   "active",
		UserStatusPending:  "pending",
		UserStatusRejected: "rejected",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("status constant mismatch: got %q want %q", got, want)
		}
	}
}
