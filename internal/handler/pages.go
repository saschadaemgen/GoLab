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
	Render    *render.Engine
	Users     *model.UserStore
	Channels  *model.ChannelStore
	Posts     *model.PostStore
	Follows   *model.FollowStore
	Reactions *model.ReactionStore
	Spaces    *model.SpaceStore
	Settings  *model.SettingsStore
	SiteName  string
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

type homeContent struct {
	TrendingChannels []model.Channel
	RecentPosts      []model.Post
}

func (h *PageHandler) Home(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user != nil {
		http.Redirect(w, r, "/feed", http.StatusFound)
		return
	}

	trending, err := h.Channels.ListPublic(r.Context(), 6, 0)
	if err != nil {
		slog.Error("home: list channels", "error", err)
		trending = nil
	}
	if trending == nil {
		trending = []model.Channel{}
	}

	data := h.newPageData(r, "GoLab - Privacy-first developer community")
	data.Content = homeContent{
		TrendingChannels: trending,
		RecentPosts:      nil,
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

// ---------- Channel ----------

type channelContent struct {
	Channel  *model.Channel
	Posts    []model.Post
	IsMember bool
}

func (h *PageHandler) ChannelPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ch, err := h.Channels.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("channel page: find", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.NotFound(w, r)
		return
	}

	posts, err := h.Posts.ListByChannel(r.Context(), ch.ID, 30, nil)
	if err != nil {
		slog.Error("channel page: list posts", "error", err)
		posts = nil
	}
	if posts == nil {
		posts = []model.Post{}
	}

	isMember := false
	if user := auth.UserFromContext(r.Context()); user != nil {
		isMember, _ = h.Channels.IsMember(r.Context(), ch.ID, user.ID)
	}

	data := h.newPageData(r, ch.Name+" - GoLab")
	data.Content = channelContent{
		Channel:  ch,
		Posts:    posts,
		IsMember: isMember,
	}
	if err := h.Render.Render(w, "channel", data); err != nil {
		slog.Error("render channel", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------- Explore ----------

type exploreContent struct {
	Channels []model.Channel
}

func (h *PageHandler) ExplorePage(w http.ResponseWriter, r *http.Request) {
	channels, err := h.Channels.ListPublic(r.Context(), 50, 0)
	if err != nil {
		slog.Error("explore page", "error", err)
		channels = nil
	}
	if channels == nil {
		channels = []model.Channel{}
	}

	data := h.newPageData(r, "Explore - GoLab")
	data.Content = exploreContent{Channels: channels}
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

	// Hide email unless viewing own profile
	if !isSelf {
		profile.Email = ""
	}

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
