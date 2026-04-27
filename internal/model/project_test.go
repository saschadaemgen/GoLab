package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Sprint 16a: structural guards for the project system. These tests
// run with `go test ./...` (no DB) and catch refactors that would
// otherwise compile but break the handler / migration contract.
// Behavioural store tests live in projects_integration_test.go behind
// the `integration` build tag.

// ============================================================
// Project struct + ProjectStore reflection guards
// ============================================================

func TestProject_RequiredFields(t *testing.T) {
	cases := map[string]reflect.Kind{
		"ID":          reflect.Int64,
		"SpaceID":     reflect.Int64,
		"Slug":        reflect.String,
		"Name":        reflect.String,
		"Description": reflect.String,
		"Status":      reflect.String,
		"Visibility":  reflect.String,
		"OwnerID":     reflect.Int64,
		"Icon":        reflect.String,
		"Color":       reflect.String,
		"CreatedAt":   reflect.Struct, // time.Time
		"UpdatedAt":   reflect.Struct,
	}
	tt := reflect.TypeOf(Project{})
	for name, kind := range cases {
		f, ok := tt.FieldByName(name)
		if !ok {
			t.Errorf("Project is missing %s", name)
			continue
		}
		if f.Type.Kind() != kind {
			t.Errorf("Project.%s kind = %s, want %s", name, f.Type.Kind(), kind)
		}
	}

	// DeletedAt is nullable - *time.Time.
	if f, ok := tt.FieldByName("DeletedAt"); !ok {
		t.Error("Project is missing DeletedAt")
	} else if f.Type.Kind() != reflect.Ptr {
		t.Errorf("Project.DeletedAt kind = %s, want Ptr", f.Type.Kind())
	}
}

func TestProjectStore_HasMethods(t *testing.T) {
	required := map[string]int{
		"Create":         3, // recv + ctx + params
		"FindByID":       3, // recv + ctx + id
		"FindBySlug":     4, // recv + ctx + spaceID + slug
		"ListBySpace":    4, // recv + ctx + spaceID + viewerID
		"ListByOwner":    3,
		"Update":         3,
		"SoftDelete":     3,
		"CanUserAccess":  4,
		"AttachTags":     4, // recv + ctx + projectID + tagIDs
		"ListTags":       3,
	}
	tt := reflect.TypeOf(&ProjectStore{})
	for name, wantIn := range required {
		m, ok := tt.MethodByName(name)
		if !ok {
			t.Errorf("ProjectStore is missing %s", name)
			continue
		}
		if m.Type.NumIn() != wantIn {
			t.Errorf("%s NumIn = %d, want %d", name, m.Type.NumIn(), wantIn)
		}
	}
}

func TestProjectStatusConstants(t *testing.T) {
	cases := map[string]string{
		ProjectStatusDraft:    "draft",
		ProjectStatusActive:   "active",
		ProjectStatusArchived: "archived",
		ProjectStatusClosed:   "closed",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("status constant: got %q want %q", got, want)
		}
	}
}

func TestProjectVisibilityConstants(t *testing.T) {
	cases := map[string]string{
		ProjectVisibilityPublic:      "public",
		ProjectVisibilityMembersOnly: "members_only",
		ProjectVisibilityHidden:      "hidden",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("visibility constant: got %q want %q", got, want)
		}
	}
}

func TestValidateProjectSlug(t *testing.T) {
	good := []string{"abc", "trust-level-engine", "go-lab", "a1-b2-c3", "x-y", strings.Repeat("a", 64)}
	for _, s := range good {
		if err := ValidateProjectSlug(s); err != nil {
			t.Errorf("ValidateProjectSlug(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",                       // empty
		"ab",                     // too short
		strings.Repeat("a", 65),  // too long
		"Abc",                    // uppercase
		"foo bar",                // space
		"foo--bar",               // double hyphen breaks the regex
		"-foo",                   // leading hyphen
		"foo-",                   // trailing hyphen
		"foo_bar",                // underscore
		"foo!",                   // special char
		"foo.bar",                // dot
	}
	for _, s := range bad {
		if err := ValidateProjectSlug(s); !errors.Is(err, ErrProjectInvalidSlug) {
			t.Errorf("ValidateProjectSlug(%q) = %v, want ErrProjectInvalidSlug", s, err)
		}
	}
}

func TestSlugifyProject(t *testing.T) {
	cases := map[string]string{
		"Trust Level Engine":  "trust-level-engine",
		"  hello  world  ":    "hello-world",
		"GoLab":               "golab",
		"A!@#B$%^C":           "a-b-c",
		"---hello---":         "hello",
		"":                    "",
	}
	for in, want := range cases {
		got := SlugifyProject(in)
		if got != want {
			t.Errorf("SlugifyProject(%q) = %q, want %q", in, got, want)
		}
	}
}

// ============================================================
// ProjectDoc reflection guards
// ============================================================

func TestProjectDoc_RequiredFields(t *testing.T) {
	required := []string{
		"ID", "ProjectID", "DocType", "Title",
		"ContentMD", "ContentHTML", "SortOrder",
		"LastEditedBy", "CreatedAt", "UpdatedAt",
	}
	tt := reflect.TypeOf(ProjectDoc{})
	for _, name := range required {
		if _, ok := tt.FieldByName(name); !ok {
			t.Errorf("ProjectDoc is missing %s", name)
		}
	}
}

func TestProjectDocStore_HasMethods(t *testing.T) {
	required := []string{"Upsert", "ListByProject", "GetByType", "GetByID", "Delete"}
	tt := reflect.TypeOf(&ProjectDocStore{})
	for _, name := range required {
		if _, ok := tt.MethodByName(name); !ok {
			t.Errorf("ProjectDocStore is missing %s", name)
		}
	}
}

func TestProjectDocConstants(t *testing.T) {
	cases := map[string]string{
		ProjectDocConcept:      "concept",
		ProjectDocArchitecture: "architecture",
		ProjectDocWorkflow:     "workflow",
		ProjectDocRoadmap:      "roadmap",
		ProjectDocCustom:       "custom",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("doc-type constant: got %q want %q", got, want)
		}
	}
}

func TestIsValidProjectDocType(t *testing.T) {
	good := []string{"concept", "architecture", "workflow", "roadmap", "custom"}
	for _, s := range good {
		if !IsValidProjectDocType(s) {
			t.Errorf("IsValidProjectDocType(%q) = false, want true", s)
		}
	}
	bad := []string{"", "Concept", "readme", "spec", "notes"}
	for _, s := range bad {
		if IsValidProjectDocType(s) {
			t.Errorf("IsValidProjectDocType(%q) = true, want false", s)
		}
	}
}

// ============================================================
// Season reflection guards
// ============================================================

func TestSeason_RequiredFields(t *testing.T) {
	required := []string{
		"ID", "ProjectID", "SeasonNumber", "Title", "Description",
		"Status", "StartedAt", "ClosedAt", "ClosingDocMD",
		"ClosingDocHTML", "CreatedAt", "UpdatedAt",
	}
	tt := reflect.TypeOf(Season{})
	for _, name := range required {
		if _, ok := tt.FieldByName(name); !ok {
			t.Errorf("Season is missing %s", name)
		}
	}
}

func TestSeasonStore_HasMethods(t *testing.T) {
	required := []string{
		"Create", "FindByID", "GetByNumber", "ListByProject",
		"UpdateMeta", "Activate", "Close",
	}
	tt := reflect.TypeOf(&SeasonStore{})
	for _, name := range required {
		if _, ok := tt.MethodByName(name); !ok {
			t.Errorf("SeasonStore is missing %s", name)
		}
	}
}

func TestSeasonStatusConstants(t *testing.T) {
	cases := map[string]string{
		SeasonStatusPlanned: "planned",
		SeasonStatusActive:  "active",
		SeasonStatusClosed:  "closed",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("season status constant: got %q want %q", got, want)
		}
	}
}

// ============================================================
// ProjectMember reflection guards
// ============================================================

func TestProjectMember_RequiredFields(t *testing.T) {
	required := []string{
		"ID", "ProjectID", "UserID", "Role", "InvitedBy", "JoinedAt",
	}
	tt := reflect.TypeOf(ProjectMember{})
	for _, name := range required {
		if _, ok := tt.FieldByName(name); !ok {
			t.Errorf("ProjectMember is missing %s", name)
		}
	}
}

func TestProjectMemberStore_HasMethods(t *testing.T) {
	required := []string{
		"Add", "Remove", "GetRole", "UpdateRole", "IsMember", "ListByProject",
	}
	tt := reflect.TypeOf(&ProjectMemberStore{})
	for _, name := range required {
		if _, ok := tt.MethodByName(name); !ok {
			t.Errorf("ProjectMemberStore is missing %s", name)
		}
	}
}

func TestProjectRoleConstants(t *testing.T) {
	cases := map[string]string{
		ProjectRoleOwner:       "owner",
		ProjectRoleContributor: "contributor",
		ProjectRoleViewer:      "viewer",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("role constant: got %q want %q", got, want)
		}
	}
}

func TestIsValidProjectRole(t *testing.T) {
	for _, s := range []string{"owner", "contributor", "viewer"} {
		if !IsValidProjectRole(s) {
			t.Errorf("IsValidProjectRole(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "admin", "Owner", "guest"} {
		if IsValidProjectRole(s) {
			t.Errorf("IsValidProjectRole(%q) = true, want false", s)
		}
	}
}
