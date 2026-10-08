package wsserver

import (
	"log/slog"
	"sync"
)

// Hub tracks connected WebSocket clients and fans out broadcast messages
// to all of them. Each client gets its own outbound buffered channel and
// a dedicated writer goroutine — this is the standard pattern for
// avoiding concurrent writes to the same net.Conn (writing from two
// goroutines at once would interleave bytes and corrupt the frame
// stream) and for making sure one slow client can't block the broadcast
// to everyone else.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	log     *slog.Logger
}

type client struct {
	conn *Conn
	send chan []byte
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		log:     log,
	}
}

// Register upgrades the request to a WebSocket connection, adds it to the
// hub, and blocks (running the client's read+write goroutines) until the
// connection closes. Call this directly from the /ws HTTP handler.
func (h *Hub) Register(conn *Conn) {
	c := &client{conn: conn, send: make(chan []byte, 32)}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	clientCount := len(h.clients)
	h.mu.Unlock()
	h.log.Info("ws: client connected", "total_clients", clientCount)

	done := make(chan struct{})
	go h.writePump(c, done)
	h.readPump(c) // blocks until the client disconnects or errors
	close(done)

	h.mu.Lock()
	delete(h.clients, c)
	remaining := len(h.clients)
	h.mu.Unlock()
	close(c.send)
	conn.Close()
	h.log.Info("ws: client disconnected", "total_clients", remaining)
}

// readPump just drains incoming messages (this is a server-push
// broadcast channel; we don't act on anything the client sends, but we
// must keep reading so ping/pong and close frames are handled, and so we
// notice when the client disconnects).
func (h *Hub) readPump(c *client) {
	for {
		_, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

func (h *Hub) writePump(c *client, done <-chan struct{}) {
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.conn.WriteMessage(msg); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// Broadcast sends msg to every currently-connected client. A client whose
// send buffer is full (32 messages behind) is skipped for this message
// rather than blocking the whole broadcast — a slow/stuck client
// degrades gracefully instead of stalling every other viewer.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			h.log.Warn("ws: client send buffer full, dropping message for this client")
		}
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
