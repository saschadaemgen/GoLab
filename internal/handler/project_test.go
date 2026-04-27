package handler

import (
	"reflect"
	"testing"
)

// Sprint 16a: structural guards for the project HTTP handler. The
// route registration in cmd/golab/main.go depends on every method
// below existing with the standard http.HandlerFunc shape - if a
// future refactor renames a method or accidentally drops one, these
// reflection checks fail before the binary boots.
//
// Behaviour tests for the underlying stores live in
// internal/model/project_integration_test.go behind the `integration`
// build tag.

func TestProjectHandler_HasMethods(t *testing.T) {
	required := []string{
		"CreateInSpace",
		"ListInSpace",
		"Get",
		"Update",
		"Delete",
		"ListDocs",
		"GetDoc",
		"UpsertDoc",
		"DeleteDoc",
		"CreateSeason",
		"ListSeasons",
		"GetSeason",
		"UpdateSeason",
		"ActivateSeason",
		"CloseSeason",
		"ListMembers",
		"AddMember",
		"UpdateMemberRole",
		"RemoveMember",
	}
	tt := reflect.TypeOf(&ProjectHandler{})
	for _, name := range required {
		m, ok := tt.MethodByName(name)
		if !ok {
			t.Errorf("ProjectHandler is missing %s", name)
			continue
		}
		// Every handler must take (recv, http.ResponseWriter, *http.Request)
		// and return nothing - the standard chi handler shape.
		if m.Type.NumIn() != 3 {
			t.Errorf("%s NumIn = %d, want 3", name, m.Type.NumIn())
		}
		if m.Type.NumOut() != 0 {
			t.Errorf("%s NumOut = %d, want 0", name, m.Type.NumOut())
		}
	}
}

func TestProjectHandler_HasRequiredFields(t *testing.T) {
	required := []string{
		"Projects",
		"ProjectDocs",
		"Seasons",
		"Members",
		"Spaces",
		"Tags",
		"Users",
		"Markdown",
		"Sanitizer",
	}
	tt := reflect.TypeOf(ProjectHandler{})
	for _, name := range required {
		if _, ok := tt.FieldByName(name); !ok {
			t.Errorf("ProjectHandler is missing %s field", name)
		}
	}
}
