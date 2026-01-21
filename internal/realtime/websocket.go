package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 4096

	// Maximum number of queued messages before dropping.
	sendBufferSize = 256

	// Rate limit: max messages per second per client.
	maxMessagesPerSecond = 10
)

// WSConfig holds WebSocket server configuration.
type WSConfig struct {
	WriteWait            time.Duration
	PongWait             time.Duration
	PingPeriod           time.Duration
	MaxMessageSize       int64
	SendBufferSize       int
	MaxMessagesPerSecond int
	AllowedOrigins       []string
}

// DefaultWSConfig returns sensible defaults.
func DefaultWSConfig() WSConfig {
	return WSConfig{
		WriteWait:            writeWait,
		PongWait:             pongWait,
		PingPeriod:           pingPeriod,
		MaxMessageSize:       maxMessageSize,
		SendBufferSize:       sendBufferSize,
		MaxMessagesPerSecond: maxMessagesPerSecond,
		AllowedOrigins:       []string{"*"}, // Configure appropriately in production
	}
}

// WSHub maintains the set of active clients and broadcasts messages.
type WSHub struct {
	clients    map[*WSClient]bool
	broadcast  chan []byte
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
	config     WSConfig
	logger     *slog.Logger
	nats       *NATSClient
	subs       []*nats.Subscription
	metrics    *WSMetrics
	upgrader   websocket.Upgrader
	cancel     context.CancelFunc
}

// WSMetrics holds WebSocket metrics.
type WSMetrics struct {
	ConnectionsTotal   atomic.Int64
	ConnectionsCurrent atomic.Int64
	MessagesSent       atomic.Int64
	MessagesReceived   atomic.Int64
	Errors             atomic.Int64
}

// NewWSMetrics creates a new WSMetrics instance.
func NewWSMetrics() *WSMetrics {
	return &WSMetrics{}
}

// WSClient represents a WebSocket client connection.
type WSClient struct {
	ID            string
	hub           *WSHub
	conn          *websocket.Conn
	send          chan []byte
	topics        map[string]bool
	mu            sync.RWMutex
	lastMessage   time.Time
	messageCount  int
	authenticated bool
	userID        string
	sessionID     string
}

// WSMessage represents a WebSocket message from/to clients.
type WSMessage struct {
	Type      string      `json:"type"`
	Topic     string      `json:"topic,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// UpdateNotification is sent to clients when new content is available.
type UpdateNotification struct {
	Type      string      `json:"type"`
	Source    string      `json:"source"`
	Message   string      `json:"message"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// CacheInvalidatedNotification is sent when cache is invalidated.
type CacheInvalidatedNotification struct {
	Type            string   `json:"type"`
	AffectedQueries []string `json:"affected_queries"`
	Timestamp       time.Time `json:"timestamp"`
}

// NewWSHub creates a new WebSocket hub.
func NewWSHub(natsClient *NATSClient, cfg WSConfig, logger *slog.Logger) *WSHub {
	if logger == nil {
		logger = slog.Default()
	}

	hub := &WSHub{
		clients:    make(map[*WSClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		config:     cfg,
		logger:     logger.With("component", "websocket_hub"),
		nats:       natsClient,
		subs:       make([]*nats.Subscription, 0),
		metrics:    NewWSMetrics(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				if len(cfg.AllowedOrigins) == 0 || cfg.AllowedOrigins[0] == "*" {
					return true
				}
				origin := r.Header.Get("Origin")
				for _, allowed := range cfg.AllowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			},
		},
	}

	return hub
}

// Start starts the WebSocket hub and NATS subscriptions.
func (h *WSHub) Start(ctx context.Context) error {
	ctx, h.cancel = context.WithCancel(ctx)

	h.logger.Info("starting WebSocket hub")

	// Subscribe to document indexed events
	js := h.nats.JetStream()
	sub, err := js.Subscribe(
		SubjectDocumentIndexed,
		func(msg *nats.Msg) {
			h.handleDocumentIndexed(msg)
		},
		nats.Durable("websocket-hub-indexed"),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to document indexed: %w", err)
	}
	h.subs = append(h.subs, sub)

	// Subscribe to cache invalidation events
	sub, err = js.Subscribe(
		SubjectCacheInvalidate,
		func(msg *nats.Msg) {
			h.handleCacheInvalidate(msg)
		},
		nats.Durable("websocket-hub-cache"),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to cache invalidate: %w", err)
	}
	h.subs = append(h.subs, sub)

	// Start the hub run loop
	go h.run(ctx)

	h.logger.Info("WebSocket hub started")
	return nil
}

// Stop gracefully stops the WebSocket hub.
func (h *WSHub) Stop(ctx context.Context) error {
	h.logger.Info("stopping WebSocket hub")

	if h.cancel != nil {
		h.cancel()
	}

	// Drain NATS subscriptions
	for _, sub := range h.subs {
		if err := sub.Drain(); err != nil {
			h.logger.Warn("failed to drain subscription", "error", err)
		}
	}

	// Close all client connections
	h.mu.Lock()
	for client := range h.clients {
		close(client.send)
		client.conn.Close()
	}
	h.clients = make(map[*WSClient]bool)
	h.mu.Unlock()

	h.logger.Info("WebSocket hub stopped")
	return nil
}

// run handles hub operations.
func (h *WSHub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.metrics.ConnectionsTotal.Add(1)
			h.metrics.ConnectionsCurrent.Add(1)
			h.logger.Debug("client registered",
				"client_id", client.ID,
				"total_clients", len(h.clients),
			)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.metrics.ConnectionsCurrent.Add(-1)
			}
			h.mu.Unlock()
			h.logger.Debug("client unregistered",
				"client_id", client.ID,
				"total_clients", len(h.clients),
			)

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
					h.metrics.MessagesSent.Add(1)
				default:
					// Client buffer full, skip this client
					h.logger.Debug("client buffer full, dropping message",
						"client_id", client.ID,
					)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// handleDocumentIndexed handles document indexed events from NATS.
func (h *WSHub) handleDocumentIndexed(msg *nats.Msg) {
	var event DocumentIndexedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		h.logger.Error("failed to unmarshal document indexed event", "error", err)
		return
	}

	notification := UpdateNotification{
		Type:      "document_updated",
		Source:    event.SourceType,
		Message:   fmt.Sprintf("New content available: %s", event.Title),
		Timestamp: event.IndexedAt,
		Data: map[string]interface{}{
			"document_id":     event.DocumentID,
			"title":           event.Title,
			"affected_topics": event.AffectedTopics,
			"source_url":      event.SourceURL,
		},
	}

	data, err := json.Marshal(notification)
	if err != nil {
		h.logger.Error("failed to marshal notification", "error", err)
		return
	}

	// Broadcast to clients subscribed to affected topics
	h.broadcastToTopics(data, event.AffectedTopics)
}

// handleCacheInvalidate handles cache invalidation events from NATS.
func (h *WSHub) handleCacheInvalidate(msg *nats.Msg) {
	var event CacheInvalidateEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		h.logger.Error("failed to unmarshal cache invalidate event", "error", err)
		return
	}

	notification := CacheInvalidatedNotification{
		Type:            "cache_invalidated",
		AffectedQueries: event.CacheKeys,
		Timestamp:       event.TriggeredAt,
	}

	data, err := json.Marshal(notification)
	if err != nil {
		h.logger.Error("failed to marshal notification", "error", err)
		return
	}

	h.broadcast <- data
}

// broadcastToTopics broadcasts a message to clients subscribed to specific topics.
func (h *WSHub) broadcastToTopics(message []byte, topics []string) {
	if len(topics) == 0 {
		// No topic filter, broadcast to all
		h.broadcast <- message
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.isSubscribedToAny(topics) {
			select {
			case client.send <- message:
				h.metrics.MessagesSent.Add(1)
			default:
				// Client buffer full
			}
		}
	}
}

// HandleWebSocket handles WebSocket upgrade and client management.
func (h *WSHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade connection", "error", err)
		h.metrics.Errors.Add(1)
		return
	}

	client := &WSClient{
		ID:            uuid.New().String(),
		hub:           h,
		conn:          conn,
		send:          make(chan []byte, h.config.SendBufferSize),
		topics:        make(map[string]bool),
		lastMessage:   time.Now(),
		authenticated: false,
	}

	// Extract session/user info from request if available
	client.sessionID = r.URL.Query().Get("session_id")
	client.userID = r.URL.Query().Get("user_id")

	// Parse initial topic subscriptions from query params
	if topics := r.URL.Query().Get("topics"); topics != "" {
		for _, topic := range splitTopics(topics) {
			client.topics[topic] = true
		}
	}

	h.register <- client

	// Start client goroutines
	go client.writePump()
	go client.readPump()

	h.logger.Info("new WebSocket client connected",
		"client_id", client.ID,
		"session_id", client.sessionID,
		"topics", len(client.topics),
	)
}

// splitTopics splits a comma-separated topic string.
func splitTopics(topics string) []string {
	if topics == "" {
		return nil
	}
	result := make([]string, 0)
	start := 0
	for i := 0; i <= len(topics); i++ {
		if i == len(topics) || topics[i] == ',' {
			if i > start {
				result = append(result, topics[start:i])
			}
			start = i + 1
		}
	}
	return result
}

// isSubscribedToAny checks if client is subscribed to any of the given topics.
func (c *WSClient) isSubscribedToAny(topics []string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// If client has no topic filters, they receive all messages
	if len(c.topics) == 0 {
		return true
	}

	for _, topic := range topics {
		if c.topics[topic] {
			return true
		}
	}
	return false
}

// Subscribe subscribes the client to a topic.
func (c *WSClient) Subscribe(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics[topic] = true
}

// Unsubscribe unsubscribes the client from a topic.
func (c *WSClient) Unsubscribe(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.topics, topic)
}

// readPump pumps messages from the WebSocket connection to the hub.
func (c *WSClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(c.hub.config.MaxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(c.hub.config.PongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(c.hub.config.PongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.hub.logger.Debug("client disconnected unexpectedly",
					"client_id", c.ID,
					"error", err,
				)
			}
			break
		}

		c.hub.metrics.MessagesReceived.Add(1)

		// Rate limiting
		now := time.Now()
		if now.Sub(c.lastMessage) < time.Second {
			c.messageCount++
			if c.messageCount > c.hub.config.MaxMessagesPerSecond {
				c.hub.logger.Debug("rate limit exceeded", "client_id", c.ID)
				continue
			}
		} else {
			c.messageCount = 1
			c.lastMessage = now
		}

		// Handle client messages
		c.handleMessage(message)
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *WSClient) writePump() {
	ticker := time.NewTicker(c.hub.config.PingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(c.hub.config.WriteWait))
			if !ok {
				// Hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Write queued messages
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte("\n"))
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(c.hub.config.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles incoming messages from clients.
func (c *WSClient) handleMessage(message []byte) {
	var msg WSMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		c.hub.logger.Debug("failed to unmarshal client message",
			"client_id", c.ID,
			"error", err,
		)
		return
	}

	switch msg.Type {
	case "subscribe":
		if msg.Topic != "" {
			c.Subscribe(msg.Topic)
			c.hub.logger.Debug("client subscribed to topic",
				"client_id", c.ID,
				"topic", msg.Topic,
			)
		}

	case "unsubscribe":
		if msg.Topic != "" {
			c.Unsubscribe(msg.Topic)
			c.hub.logger.Debug("client unsubscribed from topic",
				"client_id", c.ID,
				"topic", msg.Topic,
			)
		}

	case "ping":
		// Respond with pong
		response := WSMessage{
			Type:      "pong",
			Timestamp: time.Now(),
		}
		data, _ := json.Marshal(response)
		select {
		case c.send <- data:
		default:
		}

	default:
		c.hub.logger.Debug("unknown message type",
			"client_id", c.ID,
			"type", msg.Type,
		)
	}
}

// GetMetrics returns current WebSocket metrics.
func (h *WSHub) GetMetrics() map[string]interface{} {
	h.mu.RLock()
	clientCount := len(h.clients)
	h.mu.RUnlock()

	return map[string]interface{}{
		"connections_total":   h.metrics.ConnectionsTotal.Load(),
		"connections_current": h.metrics.ConnectionsCurrent.Load(),
		"actual_clients":      clientCount,
		"messages_sent":       h.metrics.MessagesSent.Load(),
		"messages_received":   h.metrics.MessagesReceived.Load(),
		"errors":              h.metrics.Errors.Load(),
	}
}

// BroadcastMessage sends a message to all connected clients.
func (h *WSHub) BroadcastMessage(notification interface{}) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	select {
	case h.broadcast <- data:
		return nil
	default:
		return fmt.Errorf("broadcast channel full")
	}
}

// GetClientCount returns the number of connected clients.
func (h *WSHub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
