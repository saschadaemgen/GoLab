//go:build templatecheck

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// Quick smoke test: parse templates and render each page with dummy data.
// Run: go run -tags templatecheck ./cmd/golab/templatecheck
func main() {
	eng, err := render.New("web/templates")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
		os.Exit(1)
	}

	dummyUser := &model.User{
		ID: 1, Username: "prinz", DisplayName: "Der Prinz", Bio: "Builder.", PowerLevel: 100,
	}

	dummyPostSeasonID := int64(22)
	dummyPost := model.Post{
		ID: 1, ASType: "Note", AuthorID: 1,
		Content:           "Hello GoLab, first post!",
		PostType:          "tutorial",
		ReactionCount:     3, ReplyCount: 1, RepostCount: 0,
		CreatedAt:         time.Now().Add(-15 * time.Minute),
		UpdatedAt:         time.Now(),
		AuthorUsername:    "prinz",
		AuthorDisplayName: "Der Prinz",
		SpaceName:         "SimpleX Protocol",
		SpaceSlug:         "simplex",
		SpaceColor:        "#45BDD1",
		// Sprint 16b Phase 4: season badge fields. dummyPostSeasonID
		// makes one of the rendered posts demonstrate the badge.
		SeasonID:     &dummyPostSeasonID,
		SeasonNumber: 2,
		SeasonTitle:  "Polish",
		ProjectSlug:  "trust-engine",
		ProjectName:  "Trust Level Engine",
		Tags: []model.Tag{
			{ID: 1, Name: "smp-protocol", Slug: "smp-protocol", UseCount: 7},
			{ID: 2, Name: "docker", Slug: "docker", UseCount: 5},
		},
	}

	dummySpaces := []model.Space{
		{ID: 1, Slug: "simplex", Name: "SimpleX Protocol", Color: "#45BDD1", Icon: "*", SortOrder: 1, PostCount: 12},
		{ID: 2, Slug: "matrix", Name: "Matrix / Element", Color: "#0DBD8B", Icon: "#", SortOrder: 2, PostCount: 3},
		{ID: 8, Slug: "meta", Name: "Off-Topic / Meta", Color: "#95A5A6", Icon: "-", SortOrder: 8, PostCount: 5},
	}

	// withBase injects the fields base.html needs on every page so the
	// space-bar and navbar render without {{nil}} errors.
	withBase := func(page map[string]any, currentSpace string) map[string]any {
		if _, ok := page["Spaces"]; !ok {
			page["Spaces"] = dummySpaces
		}
		if _, ok := page["CurrentSpace"]; !ok {
			page["CurrentSpace"] = currentSpace
		}
		return page
	}

	pages := map[string]any{
		"home": map[string]any{
			"Title": "Home", "SiteName": "GoLab", "User": nil, "CurrentPath": "/",
			"Content": map[string]any{
				"TrendingSpaces": dummySpaces,
				"RecentPosts":    nil,
			},
		},
		"register": map[string]any{"Title": "Register", "SiteName": "GoLab", "User": nil, "CurrentPath": "/register"},
		"login":    map[string]any{"Title": "Login", "SiteName": "GoLab", "User": nil, "CurrentPath": "/login"},
		"feed": map[string]any{
			"Title": "Feed", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/feed",
			"Content": map[string]any{
				"Posts":          []model.Post{dummyPost},
				"JoinedChannels": []model.Channel{{ID: 1, Slug: "general", Name: "General", MemberCount: 42}},
				"Suggested":      []model.Channel{{ID: 2, Slug: "gochat", Name: "GoChat", MemberCount: 18}},
			},
		},
		"explore": map[string]any{
			"Title": "Explore", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/explore",
			"Content": map[string]any{
				"Spaces": dummySpaces,
			},
		},
		"settings": map[string]any{"Title": "Settings", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/settings"},
		"profile": map[string]any{
			"Title": "Profile", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/u/prinz",
			"Content": map[string]any{
				"Profile":        dummyUser,
				"RecentPosts":    []model.Post{dummyPost},
				"FollowerCount":  5,
				"FollowingCount": 12,
				"IsFollowing":    false,
				"IsSelf":         true,
			},
		},
		"channel": map[string]any{
			"Title": "Channel", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/c/general",
			"Content": map[string]any{
				"Channel":  &model.Channel{ID: 1, Slug: "general", Name: "General", Description: "Main lobby", MemberCount: 42, ChannelType: "public", CreatedAt: time.Now().Add(-10 * 24 * time.Hour)},
				"Posts":    []model.Post{dummyPost},
				"IsMember": true,
			},
		},
		"thread": map[string]any{
			"Title": "Thread", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/p/1",
			"Content": map[string]any{
				"Post":    &dummyPost,
				"Replies": []model.Post{dummyPost},
				"Channel": &model.Channel{ID: 1, Slug: "general", Name: "General"},
			},
		},
		"admin": map[string]any{
			"Title": "Admin", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/admin",
			"Content": dummyAdminContent(dummyUser),
		},
		"space": map[string]any{
			"Title": "Space - GoLab", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/s/simplex",
			"Content": map[string]any{
				"Space":       &model.Space{ID: 1, Slug: "simplex", Name: "SimpleX Protocol", Description: "SMP protocol, clients, relays", Color: "#45BDD1", Icon: "*", SortOrder: 1, PostCount: 12},
				"Posts":       []model.Post{dummyPost},
				"PopularTags": []model.Tag{{ID: 1, Name: "smp-protocol", Slug: "smp-protocol", UseCount: 7}},
				"ActiveType":  "",
				"ActiveTag":   "",
				// Sprint 16b polish: project overview block. Empty
				// slice keeps the section hidden so the templatecheck
				// also exercises the "no projects" path.
				"Projects":            []map[string]any{},
				"ProjectActivity":     map[string]any{"hasData": false},
				"TotalProjectPosts":   int64(0),
				"TotalProjectSeasons": 0,
			},
		},
		"tag": map[string]any{
			"Title": "#docker - GoLab", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/t/docker",
			"Content": map[string]any{
				"Tag":   &model.Tag{ID: 4, Name: "docker", Slug: "docker", UseCount: 9},
				"Posts": []model.Post{dummyPost},
			},
		},
		"pending": map[string]any{
			"Title": "Account pending", "SiteName": "GoLab",
			"User": &model.User{ID: 5, Username: "newbie", DisplayName: "Newbie", Status: "pending"},
			"CurrentPath": "/pending",
		},
	}

	// Sprint 16b: project system pages. Build dummy data once so each
	// page renders against realistic shape (project + owner + tags +
	// docs + seasons + members + counts).
	dummyProjectSpace := &model.Space{
		ID: 1, Slug: "simplex", Name: "SimpleX Protocol",
		Description: "SMP protocol, clients, relays",
		Color: "#45BDD1", Icon: "*", SortOrder: 1, PostCount: 12,
	}
	dummyParentID := int64(5)
	dummyProject := &model.Project{
		ID: 7, SpaceID: 1,
		Slug: "trust-engine", Name: "Trust Level Engine",
		Description: "Implements TL0 through TL4 with Discourse semantics for the GoLab community.",
		Status:     model.ProjectStatusActive,
		Visibility: model.ProjectVisibilityPublic,
		OwnerID:    1,
		Icon:       "+", Color: "#3CDFCF",
		// Sprint 16d: dummyProject is a sub-project of GoLab so the
		// templatecheck exercises the breadcrumb + "in {parent}"
		// rendering paths.
		ParentProjectID: &dummyParentID,
		CreatedAt:       time.Now().Add(-12 * 24 * time.Hour),
		UpdatedAt:       time.Now().Add(-2 * time.Hour),
		SpaceSlug:       "simplex", SpaceName: "SimpleX Protocol",
		ParentSlug:      "golab", ParentName: "GoLab",
	}
	dummyChildProject := model.Project{
		ID: 8, SpaceID: 1,
		Slug: "reading-tracker", Name: "Reading Tracker",
		Description: "Personal reading habits and book recommendations.",
		Status:     model.ProjectStatusDraft,
		Visibility: model.ProjectVisibilityPublic,
		OwnerID:    1,
		Color:      "#9B59B6",
		ParentProjectID: &dummyParentID,
		CreatedAt:       time.Now().Add(-3 * 24 * time.Hour),
		UpdatedAt:       time.Now().Add(-1 * 24 * time.Hour),
		SpaceSlug:       "simplex", SpaceName: "SimpleX Protocol",
		ParentSlug:      "golab", ParentName: "GoLab",
	}
	dummyProjectTags := []model.Tag{
		{ID: 1, Name: "trust", Slug: "trust", UseCount: 4},
		{ID: 2, Name: "engine", Slug: "engine", UseCount: 3},
	}
	dummyDocs := []model.ProjectDoc{
		{ID: 11, ProjectID: 7, DocType: model.ProjectDocConcept, Title: "Concept",
			ContentMD: "# Concept\n\nWe need TLs.", ContentHTML: "<h1>Concept</h1><p>We need TLs.</p>",
			SortOrder: 1, CreatedAt: time.Now().Add(-10 * 24 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: 12, ProjectID: 7, DocType: model.ProjectDocArchitecture, Title: "Architecture",
			ContentMD: "Layered.", ContentHTML: "<p>Layered.</p>",
			SortOrder: 2, CreatedAt: time.Now().Add(-9 * 24 * time.Hour), UpdatedAt: time.Now().Add(-3 * 24 * time.Hour)},
	}
	startedAt := time.Now().Add(-5 * 24 * time.Hour)
	closedAt := time.Now().Add(-30 * 24 * time.Hour)
	dummySeasons := []model.Season{
		{ID: 21, ProjectID: 7, SeasonNumber: 1, Title: "Foundation",
			Description: "Initial scaffolding and store layer.",
			Status:      model.SeasonStatusClosed,
			StartedAt:   &closedAt, ClosedAt: &closedAt,
			ClosingDocMD: "# Closing\n\nDone.", ClosingDocHTML: "<h1>Closing</h1><p>Done.</p>",
			CreatedAt:   time.Now().Add(-30 * 24 * time.Hour), UpdatedAt: time.Now().Add(-29 * 24 * time.Hour)},
		{ID: 22, ProjectID: 7, SeasonNumber: 2, Title: "Polish",
			Description: "UX polish and frontend.",
			Status:      model.SeasonStatusActive,
			StartedAt:   &startedAt,
			CreatedAt:   time.Now().Add(-6 * 24 * time.Hour), UpdatedAt: time.Now().Add(-1 * 24 * time.Hour)},
	}
	dummyMembers := []model.ProjectMember{
		{ID: 31, ProjectID: 7, UserID: 1, Role: model.ProjectRoleOwner,
			JoinedAt: time.Now().Add(-12 * 24 * time.Hour),
			Username: "prinz", DisplayName: "Der Prinz"},
		{ID: 32, ProjectID: 7, UserID: 2, Role: model.ProjectRoleContributor,
			JoinedAt: time.Now().Add(-6 * 24 * time.Hour),
			Username: "wizard", DisplayName: "Der Zauberer"},
	}
	dummyDocPresence := map[string]*model.ProjectDoc{
		"concept":      &dummyDocs[0],
		"architecture": &dummyDocs[1],
	}
	dummyCurrentSeason := &dummySeasons[1]
	dummyEditedBy := int64(1)
	dummyDocs[0].LastEditedBy = &dummyEditedBy

	projectPages := map[string]map[string]any{
		"project-list": {
			"Title":       "Projects in SimpleX Protocol",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects",
			"Content": map[string]any{
				"Space":         dummyProjectSpace,
				"Projects":      []model.Project{*dummyProject},
				"Owners":        map[int64]*model.User{1: dummyUser},
				"TagsByProject": map[int64][]model.Tag{7: dummyProjectTags},
				"MemberCount":   map[int64]int{7: len(dummyMembers)},
				"StatusFilter":  "",
				"CanCreate":     true,
			},
		},
		"project-show": {
			"Title":       "Trust Level Engine - GoLab",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine",
			"Content": map[string]any{
				"Space":         dummyProjectSpace,
				"Project":       dummyProject,
				"Owner":         dummyUser,
				"Tags":          dummyProjectTags,
				"Docs":          dummyDocs,
				"Seasons":       dummySeasons,
				"Members":       dummyMembers,
				"DocPresence":   dummyDocPresence,
				"CurrentSeason": dummyCurrentSeason,
				"ActiveTab":     "overview",
				"CanEdit":       true,
				"CanManage":     true,
				// Sprint 16d sub-projects.
				"Children": []model.Project{dummyChildProject},
				"ParentStats": &model.ParentProjectStats{
					ChildCount:         2,
					ChildPostsCount:    14,
					ChildContributors:  3,
					ActiveChildSeasons: 1,
				},
				"CanCreateProject": true,
				// Sprint 16b polish dashboard data.
				"TotalPosts":        18,
				"TotalContributors": 3,
				"ActiveDays":        9,
				"DocsCompleted":     2,
				"Heatmap": map[string]any{
					"hasData": true,
					"cells": []map[string]any{
						{"date": "2026-04-15", "count": 0, "level": 0, "col": 0, "row": 0, "x": 0,  "y": 0},
						{"date": "2026-04-16", "count": 1, "level": 1, "col": 0, "row": 1, "x": 0,  "y": 14},
						{"date": "2026-04-22", "count": 4, "level": 2, "col": 1, "row": 0, "x": 14, "y": 0},
						{"date": "2026-04-25", "count": 8, "level": 3, "col": 1, "row": 3, "x": 14, "y": 42},
						{"date": "2026-04-26", "count": 12, "level": 4, "col": 1, "row": 4, "x": 14, "y": 56},
					},
					"weekTop": time.Now().Add(-83 * 24 * time.Hour),
				},
				"SeasonsChart": map[string]any{
					"hasData":         true,
					"labels":          []string{"S1", "S2"},
					"data":            []int{23, 12},
					"backgroundColor": []string{"rgba(155, 89, 182, 0.5)", "#3CDFCF"},
				},
			},
		},
		"project-docs": {
			"Title":       "Trust Level Engine docs - GoLab",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/docs",
			"Content": map[string]any{
				"Space":     dummyProjectSpace,
				"Project":   dummyProject,
				"Owner":     dummyUser,
				"Tags":      dummyProjectTags,
				"Docs":      dummyDocs,
				"ActiveTab": "docs",
				"CanEdit":   true,
				"CanManage": true,
			},
		},
		"project-doc": {
			"Title":       "Concept - Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/docs/concept",
			"Content": map[string]any{
				"Space":     dummyProjectSpace,
				"Project":   dummyProject,
				"Owner":     dummyUser,
				"Tags":      dummyProjectTags,
				"Doc":       &dummyDocs[0],
				"Editor":    dummyUser,
				"DocLabel":  "Concept",
				"ActiveTab": "docs",
				"CanEdit":   true,
				"CanManage": true,
			},
		},
		"project-seasons": {
			"Title":       "Trust Level Engine seasons - GoLab",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/seasons",
			"Content": map[string]any{
				"Space":      dummyProjectSpace,
				"Project":    dummyProject,
				"Owner":      dummyUser,
				"Tags":       dummyProjectTags,
				"Seasons":    dummySeasons,
				"PostCounts": map[int64]int{21: 23, 22: 12},
				"ActiveTab":  "seasons",
				"CanManage":  true,
			},
		},
		"project-season": {
			"Title":       "Season 2 - Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/seasons/2",
			"Content": map[string]any{
				"Space":     dummyProjectSpace,
				"Project":   dummyProject,
				"Owner":     dummyUser,
				"Tags":      dummyProjectTags,
				"Season":    &dummySeasons[1],
				"Posts":     []model.Post{dummyPost},
				"ActiveTab": "seasons",
				"CanManage": true,
				// Sprint 16b polish dashboard data.
				"PostCount":        18,
				"ContributorCount": 3,
				"DaysRunning":      27,
				"LinkedDocs":       4,
				"DailyChart": map[string]any{
					"hasData": true,
					"labels":  []string{"Apr 1", "Apr 2", "Apr 3", "Apr 4", "Apr 5"},
					"data":    []int{2, 1, 0, 4, 3},
				},
				"TypeChart": map[string]any{
					"hasData":         true,
					"labels":          []string{"discussion", "tutorial", "code"},
					"data":            []int{8, 6, 4},
					"backgroundColor": []string{"#45BDD1", "#2ECC71", "#F39C12"},
				},
			},
		},
		"project-members": {
			"Title":       "Trust Level Engine members - GoLab",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/members",
			"Content": map[string]any{
				"Space":        dummyProjectSpace,
				"Project":      dummyProject,
				"Owner":        dummyUser,
				"Tags":         dummyProjectTags,
				"Owners":       []model.ProjectMember{dummyMembers[0]},
				"Contributors": []model.ProjectMember{dummyMembers[1]},
				"Viewers":      []model.ProjectMember{},
				"ActiveTab":    "members",
				"CanManage":    true,
			},
		},
	}
	for name, page := range projectPages {
		pages[name] = page
	}

	// Sprint 16b Phase 2: project authoring forms.
	projectFormDefaults := map[string]any{
		"Name":        "",
		"Slug":        "",
		"Description": "",
		"Status":      "draft",
		"Visibility":  "public",
		"Icon":        "",
		"Color":       "",
	}
	projectFormFilled := map[string]any{
		"Name":        "Trust Level Engine",
		"Slug":        "trust-engine",
		"Description": "TL0-TL4 with Discourse semantics.",
		"Status":      "active",
		"Visibility":  "public",
		"Icon":        "+",
		"Color":       "#3CDFCF",
	}
	formPages := map[string]map[string]any{
		"project-new": {
			"Title":       "New project - SimpleX Protocol",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/new",
			"Content": map[string]any{
				"Space": dummyProjectSpace,
				"Form":  projectFormDefaults,
				"Error": "",
				// Sprint 16d parent dropdown options.
				"PotentialParents": []model.Project{
					{ID: 5, Slug: "golab", Name: "GoLab", Icon: "@"},
					{ID: 6, Slug: "gochat", Name: "GoChat"},
				},
			},
		},
		"project-edit": {
			"Title":       "Edit Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/edit",
			"Content": map[string]any{
				"Space":   dummyProjectSpace,
				"Project": dummyProject,
				"Form":    projectFormFilled,
				"Error":   "",
				"PotentialParents": []model.Project{
					{ID: 5, Slug: "golab", Name: "GoLab", Icon: "@"},
				},
				"HasChildren": false,
			},
		},
		"project-doc-edit": {
			"Title":       "Edit Concept - Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/docs/concept/edit",
			"Content": map[string]any{
				"Space":    dummyProjectSpace,
				"Project":  dummyProject,
				"Doc":      &dummyDocs[0],
				"DocType":  "concept",
				"DocLabel": "Concept",
				"Error":    "",
			},
		},
	}
	for name, page := range formPages {
		pages[name] = page
	}

	// Sprint 16b Phase 3: Season management forms.
	seasonFormPages := map[string]map[string]any{
		"project-season-new": {
			"Title":       "Plan Season - Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/seasons/new",
			"Content": map[string]any{
				"Space":      dummyProjectSpace,
				"Project":    dummyProject,
				"NextNumber": 3,
				"Form":       map[string]any{"Title": "", "Description": ""},
				"Error":      "",
			},
		},
		"project-season-close": {
			"Title":       "Close Season 2 - Trust Level Engine",
			"SiteName":    "GoLab",
			"User":        dummyUser,
			"CurrentPath": "/spaces/simplex/projects/trust-engine/seasons/2/close",
			"Content": map[string]any{
				"Space":        dummyProjectSpace,
				"Project":      dummyProject,
				"Season":       &dummySeasons[1],
				"Form":         map[string]any{},
				"Error":        "",
				"PostCount":    18,
				"Contributors": 3,
				"StartedLabel": "Apr 1, 2026",
				"SeedHTML":     "<h1>Season 2 Closing Document</h1><h2>What was built</h2><p></p>",
			},
		},
	}
	for name, page := range seasonFormPages {
		pages[name] = page
	}

	for name, data := range pages {
		// Make sure every page has the space-bar data base.html reads.
		if m, ok := data.(map[string]any); ok {
			current := ""
			if name == "space" {
				current = "simplex"
			}
			withBase(m, current)
		}
		var buf bytes.Buffer
		rw := &bufWriter{buf: &buf, headers: make(http.Header)}
		if err := eng.Render(rw, name, data); err != nil {
			fmt.Fprintf(os.Stderr, "render page %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("page     %-10s %7d bytes\n", name, buf.Len())
	}

	// Fragment: post-card (used by WebSocket for new_post events)
	var fbuf bytes.Buffer
	if err := eng.RenderFragmentTo(&fbuf, "post-card.html", map[string]any{
		"Post": &dummyPost,
		"User": nil,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "render fragment post-card: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("fragment %-10s %7d bytes\n", "post-card", fbuf.Len())

	// Fragment: feed-posts
	fbuf.Reset()
	if err := eng.RenderFragmentTo(&fbuf, "feed-posts.html", map[string]any{
		"Posts": []model.Post{dummyPost, dummyPost},
		"User":  dummyUser,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "render fragment feed-posts: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("fragment %-10s %7d bytes\n", "feed-posts", fbuf.Len())

	fmt.Println("all templates render OK")
}

type bufWriter struct {
	buf     *bytes.Buffer
	headers http.Header
}

func (w *bufWriter) Header() http.Header         { return w.headers }
func (w *bufWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *bufWriter) WriteHeader(_ int)           {}

// dummyAdminContent assembles the structured payload the admin page
// expects, including a Sprint X applicant with five application
// fields and a Sprint Y rating row. Exercising the pending-users
// branch makes the templatecheck catch breakage in the new
// rating-widget partial and the rating helpers (ratingDim,
// ratingAverage, ratingCount, ratingNotesJS).
func dummyAdminContent(viewer *model.User) map[string]any {
	score := func(n int) *int { return &n }
	pending := []map[string]any{
		{
			"User": model.User{
				ID:                    7,
				Username:              "applicant",
				DisplayName:           "Maria Applicant",
				PowerLevel:            10,
				Status:                model.UserStatusPending,
				CreatedAt:             time.Now().Add(-3 * time.Hour),
				UpdatedAt:             time.Now().Add(-3 * time.Hour),
				ExternalLinks:         "https://github.com/applicant https://example.dev",
				EcosystemConnection:   "I run a SimpleGo node and contribute hardware notes.",
				CommunityContribution: "Hardware integration write-ups, security reviews.",
				CurrentFocus:          "Cross-compiling SimpleGoX for ARM SBCs.",
				ApplicationNotes:      "Available for code review on weekends.",
				// Sprint Y.1 knowledge questions
				TechnicalDepthChoice: "a",
				TechnicalDepthAnswer: "Double Ratchet's main weak point is the post-compromise recovery window: an attacker who briefly captured a chain key sees every message until the next ratchet step. With high-latency channels this gap matters.",
				PracticalExperience:  "Yes - I run a SimpleX SMP relay on a small SBC for personal contacts.",
				CriticalThinking:     "Telegram's marketing of 'secret chats' as the same product as default cloud chats - they are not, and most users never enable secret chats.",
			},
			"Rating": &model.ApplicationRating{
				UserID:                7,
				TrackRecord:           score(8),
				EcosystemFit:          score(9),
				ContributionPotential: score(7),
				Notes:                 "Strong portfolio.",
				UpdatedAt:             time.Now().Add(-30 * time.Minute),
			},
		},
	}
	return map[string]any{
		"Stats": map[string]any{"Users": 42, "Posts": 187, "Spaces": 8, "Banned": 0},
		"Users": []map[string]any{
			{"ID": int64(1), "Username": "prinz", "DisplayName": "Der Prinz", "PowerLevel": 100, "PostCount": 3, "Banned": false, "CreatedAt": time.Now().Add(-2 * time.Hour)},
		},
		"PendingUsers":        pending,
		"RequireApproval":     true,
		"AllowUsernameChange": true,
	}
}
