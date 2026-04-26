package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// PageData is the common envelope every page template receives.
//
// Spaces + CurrentSpace power the space bar rendered by base.html on
// every page. Keeping them on PageData means each page handler gets
// them "for free" via newPageData() instead of every handler having
// to remember to populate them.
type PageData struct {
	Title        string
	SiteName     string
	User         *model.User // nil if not logged in
	CurrentPath  string
	Spaces       []model.Space
	CurrentSpace string // slug of the active space (empty on /feed, /u/..., etc.)
	Content      any
}

type PageHandler struct {
	Render      *render.Engine
	Users       *model.UserStore
	Channels    *model.ChannelStore
	Posts       *model.PostStore
	Follows     *model.FollowStore
	Reactions   *model.ReactionStore
	Spaces      *model.SpaceStore
	Settings    *model.SettingsStore
	EditHistory *model.PostEditHistoryStore // Sprint 15a B6
	SiteName    string
}

func (h *PageHandler) newPageData(r *http.Request, title string) PageData {
	data := PageData{
		Title:       title,
		SiteName:    h.SiteName,
		User:        auth.UserFromContext(r.Context()),
		CurrentPath: r.URL.Path,
	}
	if h.Spaces != nil {
		if spaces, err := h.Spaces.List(r.Context()); err == nil {
			data.Spaces = spaces
		}
	}
	return data
}

// ---------- Home ----------

// homeContent carries the data the landing page shows to logged-out
// visitors. TrendingSpaces replaces the old TrendingChannels list -
// Spaces are the admin-curated topic areas users actually navigate
// by. Channels stay in the DB (feeds and posts still reference a
// channel_id for legacy rows) but are no longer surfaced in the UI.
type homeContent struct {
	TrendingSpaces []model.Space
	RecentPosts    []model.Post
}

func (h *PageHandler) Home(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user != nil {
		http.Redirect(w, r, "/feed", http.StatusFound)
		return
	}

	var trending []model.Space
	if h.Spaces != nil {
		spaces, err := h.Spaces.List(r.Context())
		if err != nil {
			slog.Error("home: list spaces", "error", err)
		} else {
			trending = spaces
		}
	}
	if trending == nil {
		trending = []model.Space{}
	}

	data := h.newPageData(r, "GoLab - Privacy-first developer community")
	data.Content = homeContent{
		TrendingSpaces: trending,
		RecentPosts:    nil,
	}
	if err := h.Render.Render(w, "home", data); err != nil {
		slog.Error("render home", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Register / Login ----------

type authContent struct {
	Error string
	// Flash is a short info banner shown above the form for non-error
	// states (e.g. "password-changed", "logged-out"). The template
	// whitelists known values so a crafted ?msg= can't inject content.
	Flash string
}

func (h *PageHandler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if auth.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusFound)
		return
	}
	data := h.newPageData(r, "Join GoLab")
	data.Content = authContent{Error: r.URL.Query().Get("error")}
	if err := h.Render.Render(w, "register", data); err != nil {
		slog.Error("render register", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *PageHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if auth.UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/feed", http.StatusFound)
		return
	}
	data := h.newPageData(r, "Login to GoLab")
	data.Content = authContent{
		Error: r.URL.Query().Get("error"),
		Flash: r.URL.Query().Get("msg"),
	}
	if err := h.Render.Render(w, "login", data); err != nil {
		slog.Error("render login", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Feed ----------

type feedContent struct {
	Posts          []model.Post
	JoinedChannels []model.Channel
	Suggested      []model.Channel
	// Sprint 10.5: the feed's sidebar partial receives feedContent as
	// its template dot, so $.Spaces inside that partial binds to
	// feedContent.Spaces (not to the outer PageData). Populate these
	// here so the sidebar actually gets the space list.
	Spaces       []model.Space
	CurrentSpace string
}

func (h *PageHandler) FeedPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	posts, err := h.Posts.Feed(r.Context(), user.ID, 30, nil)
	if err != nil {
		slog.Error("feed page: fetch feed", "error", err)
		posts = nil
	}
	if posts == nil {
		posts = []model.Post{}
	}
	// Sprint 14: one batch query for reaction state across the page.
	if h.Reactions != nil {
		if err := h.Reactions.AttachTo(r.Context(), user.ID, posts); err != nil {
			slog.Warn("feed page: attach reactions", "error", err)
		}
	}
	if h.EditHistory != nil {
		if err := h.EditHistory.AttachEditedAt(r.Context(), posts); err != nil {
			slog.Warn("feed page: attach edited_at", "error", err)
		}
	}

	joined, err := h.Channels.ListForUser(r.Context(), user.ID, 10)
	if err != nil {
		slog.Error("feed page: user channels", "error", err)
		joined = nil
	}
	if joined == nil {
		joined = []model.Channel{}
	}

	suggested, err := h.Channels.ListPublic(r.Context(), 5, 0)
	if err != nil {
		suggested = nil
	}
	if suggested == nil {
		suggested = []model.Channel{}
	}

	data := h.newPageData(r, "Feed - GoLab")
	data.Content = feedContent{
		Posts:          posts,
		JoinedChannels: joined,
		Suggested:      suggested,
		Spaces:         data.Spaces, // forward PageData.Spaces into the content dot
		CurrentSpace:   "",          // feed isn't scoped to a single space
	}
	if err := h.Render.Render(w, "feed", data); err != nil {
		slog.Error("render feed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Channel (legacy redirect) ----------

// ChannelPage used to render a per-channel feed. Sprint 10.5 hid
// channels from the UI in favour of Spaces; Sprint 13 finishes the
// job by redirecting every /c/{slug} URL to /feed so old links and
// bookmarks no longer dead-end on a 404 or show stale UI. The
// channel tables and APIs still exist for back-compat and Phase 2
// migration, but the HTML page is gone.
func (h *PageHandler) ChannelPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/feed", http.StatusFound)
}

// ---------- Explore ----------

// exploreContent is the "discover something to read" page. Since
// Sprint 10.5 this is organised by Space (admin-curated topic area)
// rather than by Channel. The 8 Spaces are stable, numbered, and
// live in the header; the explore page shows the same set as big
// cards with description and post count.
type exploreContent struct {
	Spaces []model.Space
}

func (h *PageHandler) ExplorePage(w http.ResponseWriter, r *http.Request) {
	var spaces []model.Space
	if h.Spaces != nil {
		list, err := h.Spaces.List(r.Context())
		if err != nil {
			slog.Error("explore page: list spaces", "error", err)
		} else {
			spaces = list
		}
	}
	if spaces == nil {
		spaces = []model.Space{}
	}

	data := h.newPageData(r, "Explore - GoLab")
	data.Content = exploreContent{Spaces: spaces}
	if err := h.Render.Render(w, "explore", data); err != nil {
		slog.Error("render explore", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Thread (single post + replies) ----------

type threadContent struct {
	Post    *model.Post
	Replies []model.Post
	Channel *model.Channel
}

func (h *PageHandler) ThreadPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	post, err := h.Posts.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("thread: find post", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if post == nil {
		http.NotFound(w, r)
		return
	}

	replies, err := h.Posts.ListReplies(r.Context(), id, 200)
	if err != nil {
		slog.Error("thread: list replies", "error", err)
		replies = nil
	}
	if replies == nil {
		replies = []model.Post{}
	}

	var ch *model.Channel
	if post.ChannelID != nil {
		ch, _ = h.Channels.FindByID(r.Context(), *post.ChannelID)
	}

	// Sprint 14: attach reaction state to the root post + every
	// reply. The root goes through a singleton slice because
	// AttachTo only accepts a []Post.
	// Sprint 15a B6: edited_at batched the same way.
	if h.Reactions != nil || h.EditHistory != nil {
		var viewerID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			viewerID = u.ID
		}
		rootWrap := []model.Post{*post}
		if h.Reactions != nil {
			// Sprint 15a B8 Nit 1: log root-post attach errors the
			// same way as the reply branch right below. Silencing
			// them with `_ =` had no justification beyond "the
			// template will still render" - when the call does
			// fail the root card silently loses its reaction
			// counts and the failure never surfaces.
			if err := h.Reactions.AttachTo(r.Context(), viewerID, rootWrap); err != nil {
				slog.Warn("thread: attach reactions root", "error", err, "post", post.ID)
			}
			if err := h.Reactions.AttachTo(r.Context(), viewerID, replies); err != nil {
				slog.Warn("thread: attach reactions", "error", err)
			}
		}
		if h.EditHistory != nil {
			if err := h.EditHistory.AttachEditedAt(r.Context(), rootWrap); err != nil {
				slog.Warn("thread: attach edited_at root", "error", err, "post", post.ID)
			}
			if err := h.EditHistory.AttachEditedAt(r.Context(), replies); err != nil {
				slog.Warn("thread: attach edited_at", "error", err)
			}
		}
		post = &rootWrap[0]
	}

	data := h.newPageData(r, "Thread - GoLab")
	data.Content = threadContent{Post: post, Replies: replies, Channel: ch}
	if err := h.Render.Render(w, "thread", data); err != nil {
		slog.Error("render thread", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Pending (Sprint 12) ----------
//
// Shown after registration when require_approval is on, and any time
// a pending user hits a mutation path. Read-only pages (feed, spaces,
// etc.) stay accessible so the user can still explore.

func (h *PageHandler) PendingPage(w http.ResponseWriter, r *http.Request) {
	data := h.newPageData(r, "Account pending - GoLab")
	if err := h.Render.Render(w, "pending", data); err != nil {
		slog.Error("render pending", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Settings ----------

// settingsContent exposes the handful of platform flags the settings
// page needs to decide what to show (username editor gated by
// allow_username_change for non-admins) plus the ?error / ?status flash
// states from POST redirects.
type settingsContent struct {
	AllowUsernameChange bool
	Error               string
	Status              string
}

func (h *PageHandler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	data := h.newPageData(r, "Settings - GoLab")
	allow := true
	if h.Settings != nil {
		allow = h.Settings.GetBool(r.Context(), "allow_username_change")
	}
	data.Content = settingsContent{
		AllowUsernameChange: allow,
		Error:               r.URL.Query().Get("error"),
		Status:              r.URL.Query().Get("status"),
	}
	if err := h.Render.Render(w, "settings", data); err != nil {
		slog.Error("render settings", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Profile ----------

type profileContent struct {
	Profile        *model.User
	RecentPosts    []model.Post
	FollowerCount  int
	FollowingCount int
	IsFollowing    bool
	IsSelf         bool
}

func (h *PageHandler) ProfilePage(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	profile, err := h.Users.FindByUsername(r.Context(), username)
	if err != nil {
		slog.Error("profile page: find user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		http.NotFound(w, r)
		return
	}

	recent, err := h.Posts.ListByAuthor(r.Context(), profile.ID, 20, nil)
	if err != nil {
		slog.Error("profile page: list posts", "error", err)
		recent = nil
	}
	if recent == nil {
		recent = []model.Post{}
	}
	if h.Reactions != nil {
		var viewerID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			viewerID = u.ID
		}
		if err := h.Reactions.AttachTo(r.Context(), viewerID, recent); err != nil {
			slog.Warn("profile page: attach reactions", "error", err)
		}
	}
	if h.EditHistory != nil {
		if err := h.EditHistory.AttachEditedAt(r.Context(), recent); err != nil {
			slog.Warn("profile page: attach edited_at", "error", err)
		}
	}

	followerCount, _ := h.Follows.FollowerCount(r.Context(), profile.ID)
	followingCount, _ := h.Follows.FollowingCount(r.Context(), profile.ID)

	isSelf := false
	isFollowing := false
	if current := auth.UserFromContext(r.Context()); current != nil {
		if current.ID == profile.ID {
			isSelf = true
		} else {
			isFollowing, _ = h.Follows.IsFollowing(r.Context(), current.ID, profile.ID)
		}
	}

	// Sprint X: email field removed. Application fields stay on the
	// user row but never reach the public profile page; they are
	// moderation-only data shown in the admin pending-users panel.
	// Strip them defensively even on self-view to avoid leaking the
	// application content into a generic profile fragment that
	// might be shared / inspected by third parties.
	profile.ExternalLinks = ""
	profile.EcosystemConnection = ""
	profile.CommunityContribution = ""
	profile.CurrentFocus = ""
	profile.ApplicationNotes = ""

	data := h.newPageData(r, profile.Username+" - GoLab")
	data.Content = profileContent{
		Profile:        profile,
		RecentPosts:    recent,
		FollowerCount:  followerCount,
		FollowingCount: followingCount,
		IsFollowing:    isFollowing,
		IsSelf:         isSelf,
	}
	if err := h.Render.Render(w, "profile", data); err != nil {
		slog.Error("render profile", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
