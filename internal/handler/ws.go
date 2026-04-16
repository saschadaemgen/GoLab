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
