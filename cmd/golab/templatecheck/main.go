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
				"TrendingChannels": []model.Channel{{ID: 1, Slug: "general", Name: "General", MemberCount: 42, ChannelType: "public"}},
				"RecentPosts":      nil,
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
				"Channels": []model.Channel{{ID: 1, Slug: "general", Name: "General", Description: "Main lobby", MemberCount: 42, ChannelType: "public"}},
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
			"Content": map[string]any{
				"Stats": map[string]any{"Users": 42, "Posts": 187, "Channels": 8, "Banned": 0},
				"Users": []map[string]any{
					{"ID": int64(1), "Username": "prinz", "DisplayName": "Der Prinz", "PowerLevel": 100, "PostCount": 3, "Banned": false, "CreatedAt": time.Now().Add(-2 * time.Hour)},
				},
				"Channels": []map[string]any{
					{"ID": int64(1), "Slug": "general", "Name": "General", "ChannelType": "public", "MemberCount": 42, "PostCount": 12, "CreatedAt": time.Now().Add(-3 * 24 * time.Hour)},
				},
			},
		},
		"space": map[string]any{
			"Title": "Space - GoLab", "SiteName": "GoLab", "User": dummyUser, "CurrentPath": "/s/simplex",
			"Content": map[string]any{
				"Space":       &model.Space{ID: 1, Slug: "simplex", Name: "SimpleX Protocol", Description: "SMP protocol, clients, relays", Color: "#45BDD1", Icon: "*", SortOrder: 1, PostCount: 12},
				"Posts":       []model.Post{dummyPost},
				"PopularTags": []model.Tag{{ID: 1, Name: "smp-protocol", Slug: "smp-protocol", UseCount: 7}},
				"ActiveType":  "",
				"ActiveTag":   "",
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
