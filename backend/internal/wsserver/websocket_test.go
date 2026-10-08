package wsserver

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- minimal client-side WebSocket implementation, used ONLY by these
// tests, to drive the real server implementation over a real TCP socket.
// Client frames must be masked per RFC 6455 §5.3 (server frames are not).

func dialWebSocket(t *testing.T, url string) net.Conn {
	t.Helper()
	// url is like "ws://127.0.0.1:PORT/ws" -> extract host:port
	host := url[len("ws://"):]
	if idx := indexSlash(host); idx != -1 {
		host = host[:idx]
	}

	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake failed: %v", err)
	}

	// read the HTTP 101 response line-by-line until the blank line
	r := bufio.NewReader(conn)
	statusLine, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status line failed: %v", err)
	}
	if statusLine[:12] != "HTTP/1.1 101" {
		t.Fatalf("expected 101 Switching Protocols, got: %q", statusLine)
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading headers failed: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	return &bufferedConnWrapper{Conn: conn, r: r}
}

// bufferedConnWrapper lets us keep using the bufio.Reader that already
// consumed the handshake bytes (there could be pipelined frame data right
// after the header block in the same TCP segment).
type bufferedConnWrapper struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConnWrapper) Read(p []byte) (int, error) { return b.r.Read(p) }

func indexSlash(s string) int {
	for i, c := range s {
		if c == '/' {
			return i
		}
	}
	return -1
}

// writeClientTextFrame sends a masked text frame, as a real browser client would.
func writeClientTextFrame(conn net.Conn, payload []byte) error {
	var mask [4]byte
	rand.Read(mask[:])
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	n := len(payload)
	var header []byte
	switch {
	case n <= 125:
		header = []byte{0x81, 0x80 | byte(n)} // FIN+text, MASK bit set
	default:
		header = make([]byte, 4)
		header[0] = 0x81
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:], uint16(n))
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	if _, err := conn.Write(mask[:]); err != nil {
		return err
	}
	_, err := conn.Write(masked)
	return err
}

// readServerTextFrame reads one unmasked frame from the server (server
// frames are never masked).
func readServerTextFrame(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, err
	}
	length := int64(head[1] & 0x7F)
	switch length {
	case 126:
		ext := make([]byte, 2)
		io.ReadFull(br, ext)
		length = int64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		io.ReadFull(br, ext)
		length = int64(binary.BigEndian.Uint64(ext))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func TestWebSocket_HandshakeAndBroadcastRoundTrip(t *testing.T) {
	hub := NewHub(slog.Default())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		hub.Register(conn)
	}))
	defer srv.Close()

	wsURL := "ws://" + srv.Listener.Addr().String() + "/ws"
	conn := dialWebSocket(t, wsURL)
	defer conn.Close()

	// give the hub a moment to register the client before broadcasting
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 registered client, got %d", hub.ClientCount())
	}

	hub.Broadcast([]byte(`{"type":"test_event"}`))

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload, err := readServerTextFrame(conn)
	if err != nil {
		t.Fatalf("reading broadcast frame failed: %v", err)
	}
	if string(payload) != `{"type":"test_event"}` {
		t.Errorf("got %q, want %q", payload, `{"type":"test_event"}`)
	}
}

func TestWebSocket_BroadcastReachesMultipleClients(t *testing.T) {
	hub := NewHub(slog.Default())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r)
		if err != nil {
			return
		}
		hub.Register(conn)
	}))
	defer srv.Close()
	wsURL := "ws://" + srv.Listener.Addr().String() + "/ws"

	const n = 3
	conns := make([]net.Conn, n)
	for i := 0; i < n; i++ {
		conns[i] = dialWebSocket(t, wsURL)
		defer conns[i].Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != n {
		t.Fatalf("expected %d registered clients, got %d", n, hub.ClientCount())
	}

	hub.Broadcast([]byte("hello-all"))

	for i, c := range conns {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		payload, err := readServerTextFrame(c)
		if err != nil {
			t.Fatalf("client %d: reading frame failed: %v", i, err)
		}
		if string(payload) != "hello-all" {
			t.Errorf("client %d: got %q, want %q", i, payload, "hello-all")
		}
	}
}

func TestWebSocket_ClientDisconnectRemovesFromHub(t *testing.T) {
	hub := NewHub(slog.Default())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r)
		if err != nil {
			return
		}
		hub.Register(conn)
	}))
	defer srv.Close()
	wsURL := "ws://" + srv.Listener.Addr().String() + "/ws"

	conn := dialWebSocket(t, wsURL)

	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client before disconnect, got %d", hub.ClientCount())
	}

	conn.Close()

	deadline = time.Now().Add(2 * time.Second)
	for hub.ClientCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", hub.ClientCount())
	}
}

func TestWebSocket_ClientCanSendMessages(t *testing.T) {
	// Even though this is a server-push broadcast design, the read pump
	// must correctly drain client->server frames (and their masking)
	// without corrupting the connection.
	hub := NewHub(slog.Default())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r)
		if err != nil {
			return
		}
		hub.Register(conn)
	}))
	defer srv.Close()
	wsURL := "ws://" + srv.Listener.Addr().String() + "/ws"

	conn := dialWebSocket(t, wsURL)
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if err := writeClientTextFrame(conn, []byte("ping from client")); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	// prove the connection is still healthy after the server read that
	// frame: broadcast something and confirm this client still receives it
	time.Sleep(100 * time.Millisecond)
	hub.Broadcast([]byte("still-alive"))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload, err := readServerTextFrame(conn)
	if err != nil {
		t.Fatalf("reading post-client-message broadcast failed: %v", err)
	}
	if string(payload) != "still-alive" {
		t.Errorf("got %q, want %q", payload, "still-alive")
	}
}

func TestComputeAccept_KnownRFC6455Example(t *testing.T) {
	// This exact key/accept pair is the worked example from RFC 6455 §1.3.
	got := computeAccept("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("computeAccept() = %q, want %q (RFC 6455 worked example)", got, want)
	}
}
