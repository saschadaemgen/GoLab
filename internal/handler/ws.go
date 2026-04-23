package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// Hub owns all active WebSocket clients and dispatches messages.
//
// Topology:
//
//	global     -> everybody (new public posts, announcements)
//	channel:X  -> members currently subscribed to a channel
//	user:N     -> notifications for a specific user
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
	topics  map[string]map[*Client]bool

	register   chan *Client
	unregister chan *Client
	broadcast  chan hubMessage

	render *render.Engine
}

type hubMessage struct {
	topic   string
	payload []byte
}

// Message is what gets serialized over the wire.
type Message struct {
	Type    string `json:"type"`              // "new_post", "notification", "reaction", "pong"
	Topic   string `json:"topic,omitempty"`   // originating topic
	HTML    string `json:"html,omitempty"`    // pre-rendered fragment
	Data    any    `json:"data,omitempty"`    // structured payload
	Message string `json:"message,omitempty"` // human-readable text (toasts)
}

// clientMessage is what we accept from clients.
type clientMessage struct {
	Type  string `json:"type"`            // "subscribe", "unsubscribe", "ping"
	Topic string `json:"topic,omitempty"` // e.g. "channel:general"
}

func NewHub(r *render.Engine) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		topics:     make(map[string]map[*Client]bool),
		register:   make(chan *Client, 16),
		unregister: make(chan *Client, 16),
		broadcast:  make(chan hubMessage, 64),
		render:     r,
	}
}

// Run is the hub's event loop. Launch once in a goroutine.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
			slog.Info("ws client connected", "total", len(h.clients))

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				for topic, set := range h.topics {
					delete(set, c)
					if len(set) == 0 {
						delete(h.topics, topic)
					}
				}
				close(c.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			set := h.topics[msg.topic]
			for c := range set {
				select {
				case c.send <- msg.payload:
				default:
					// client is too slow, drop them
					go func(cc *Client) { h.unregister <- cc }(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// PublishToUser fans msg out to every WebSocket a specific user has open.
// Clients auto-subscribe to "user:N" on connect.
func (h *Hub) PublishToUser(userID int64, msg Message) {
	h.Publish(fmt.Sprintf("user:%d", userID), msg)
}

// Publish encodes msg as JSON and sends it to all subscribers of topic.
func (h *Hub) Publish(topic string, msg Message) {
	msg.Topic = topic
	b, err := json.Marshal(msg)
	if err != nil {
		slog.Error("ws marshal", "error", err)
		return
	}
	select {
	case h.broadcast <- hubMessage{topic: topic, payload: b}:
	default:
		slog.Warn("ws broadcast queue full, dropping", "topic", topic)
	}
}

// PublishNewPost renders the post-card fragment and fans it out to subscribers
// of the global topic and (if the post targets one) the channel topic.
//
// Sprint 15a B7 Bug 1: the broadcast fragment is rendered with User: nil
// on purpose. Every WS subscriber sees the same bytes, but the owner
// dropdown gate in post-card.html (`{{ if and $currentUser (eq $currentUser.ID
// $post.AuthorID) }}`) requires the viewer's own user context, which the
// broadcast fan-out does not know (one render, N viewers). The author's
// own client does NOT rely on the broadcast; PostHandler.Create renders
// a second copy with the author's user context via RenderPostCard and
// returns it in the POST response, and the author's submit handler
// prepends that own-context copy BEFORE the WS echo arrives. The
// self-echo guard in golab.js injectNewPost then drops the broadcast
// duplicate for the author. Non-authors see exactly the viewer-neutral
// version, which is correct since they cannot edit/delete.
func (h *Hub) PublishNewPost(post *model.Post, channelSlug string) {
	if h.render == nil {
		return
	}
	var buf bytes.Buffer
	err := h.render.RenderFragmentTo(&buf, "post-card.html", map[string]any{
		"Post": post,
		"User": nil,
	})
	if err != nil {
		slog.Error("ws render post-card", "error", err)
		return
	}
	msg := Message{Type: "new_post", HTML: buf.String()}
	h.Publish("global", msg)
	if channelSlug != "" {
		h.Publish("channel:"+channelSlug, msg)
	}
}

// RenderPostCard renders a post-card fragment with the given user context.
// Returns an empty string (logged) on failure. Sprint 15a B7 Bug 1: the
// caller is PostHandler.Create, which feeds the author-context output
// back into the REST response so the author's own feed shows the
// edit/delete dropdown immediately without waiting on the WS broadcast
// that is rendered anonymously.
func (h *Hub) RenderPostCard(post *model.Post, user *model.User) string {
	if h.render == nil {
		return ""
	}
	var buf bytes.Buffer
	err := h.render.RenderFragmentTo(&buf, "post-card.html", map[string]any{
		"Post": post,
		"User": user,
	})
	if err != nil {
		slog.Error("ws render post-card with user", "error", err)
		return ""
	}
	return buf.String()
}

// PublishPostUpdated broadcasts an edit so every open feed can swap in
// the new rendered HTML and refresh the "edited" badge without reload.
// Sprint 15a B7 Bug 3: the edit path previously skipped the hub, so a
// user viewing the post on a second tab / device saw stale text until
// they refreshed. Matches the shape of PublishPostDeleted (structured
// data payload keyed by post id) rather than PublishNewPost (HTML-
// blob payload): the card already exists in the DOM on every viewer,
// we only need to update the content block and the edited-at badge,
// so we ship the rendered HTML and the edit timestamp as data and let
// the client patch the card in place. This sidesteps the user-context
// dropdown issue from Bug 1 because we're not re-rendering the whole
// card - the dropdown is already where it belongs from the original
// render.
func (h *Hub) PublishPostUpdated(post *model.Post, channelSlug string) {
	if post == nil {
		return
	}
	var editedAt int64
	if post.EditedAt != nil {
		editedAt = post.EditedAt.Unix()
	}
	msg := Message{
		Type: "post_updated",
		Data: map[string]any{
			"id":           post.ID,
			"content_html": post.ContentHTML,
			"edited_at":    editedAt,
		},
	}
	h.Publish("global", msg)
	if channelSlug != "" {
		h.Publish("channel:"+channelSlug, msg)
	}
}

// PublishPostDeleted tells every open feed to remove the card for
// postID from its DOM. Sprint 15a B5: before this existed, a
// user's delete would succeed server-side but the zombie card
// stayed visible on every other open browser until the viewer
// hit refresh. channelSlug mirrors PublishNewPost so a deletion
// reaches the same rooms the creation reached.
func (h *Hub) PublishPostDeleted(postID int64, channelSlug string) {
	msg := Message{
		Type: "post_deleted",
		Data: map[string]any{"id": postID},
	}
	h.Publish("global", msg)
	if channelSlug != "" {
		h.Publish("channel:"+channelSlug, msg)
	}
}

// subscribe/unsubscribe manage per-topic client sets.
func (h *Hub) subscribe(c *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.topics[topic]; !ok {
		h.topics[topic] = make(map[*Client]bool)
	}
	h.topics[topic][c] = true
}

func (h *Hub) unsubscribe(c *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.topics[topic]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.topics, topic)
		}
	}
}

// Client represents one live WebSocket connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	user   *model.User // may be nil for anonymous viewers
	userID int64       // 0 for anonymous
}

// HandleWS upgrades the HTTP connection and runs the client.
func (h *Hub) HandleWS(sessions *auth.SessionStore, users *model.UserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // dev only; tighten via OriginPatterns in prod
		})
		if err != nil {
			slog.Warn("ws accept", "error", err)
			return
		}

		client := &Client{
			hub:  h,
			conn: conn,
			send: make(chan []byte, 16),
		}
		if u := auth.CurrentUser(r, sessions, users); u != nil {
			client.user = u
			client.userID = u.ID
		}

		h.register <- client
		// Auto-subscribe to global and user-specific topic
		h.subscribe(client, "global")
		if client.userID != 0 {
			h.subscribe(client, fmt.Sprintf("user:%d", client.userID))
		}

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		go client.writeLoop(ctx)
		client.readLoop(ctx)
	}
}

func (c *Client) readLoop(ctx context.Context) {
	defer func() { c.hub.unregister <- c }()

	c.conn.SetReadLimit(1 << 15) // 32 KB
	for {
		var msg clientMessage
		err := wsjson.Read(ctx, c.conn, &msg)
		if err != nil {
			return
		}
		switch msg.Type {
		case "subscribe":
			if msg.Topic != "" {
				c.hub.subscribe(c, msg.Topic)
			}
		case "unsubscribe":
			if msg.Topic != "" {
				c.hub.unsubscribe(c, msg.Topic)
			}
		case "ping":
			// echo back; the writer also pings periodically
			select {
			case c.send <- []byte(`{"type":"pong"}`):
			default:
			}
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case payload, ok := <-c.send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				return
			}

		case <-ticker.C:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Ping(wctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
