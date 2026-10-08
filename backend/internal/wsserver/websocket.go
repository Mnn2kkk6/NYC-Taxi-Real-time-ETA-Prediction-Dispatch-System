// Package wsserver implements just enough of RFC 6455 (the WebSocket
// protocol) to serve a broadcast channel over /ws — no external
// dependency (gorilla/websocket, golang.org/x/net/websocket), same
// constraint as the Redis RESP client: no network route to fetch
// third-party modules in this sandbox. WebSocket isn't in Go's standard
// library at all, so this is a genuinely useful, from-scratch protocol
// implementation — worth understanding for the interview, not just a
// workaround.
//
// What's implemented: the opening handshake (Sec-WebSocket-Accept
// computation), and text/close/ping/pong frames, both directions.
// What's NOT implemented (not needed for a server-push broadcast use
// case): fragmented messages, per-message compression, binary frames.
package wsserver

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// magic GUID from RFC 6455 §1.3 — concatenated with the client's
// Sec-WebSocket-Key and SHA-1 hashed to prove the server understood the
// upgrade request (not for security, just protocol compliance).
const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

// Conn wraps a hijacked TCP connection after a successful WebSocket
// handshake. ReadMessage/WriteMessage handle RFC 6455 framing.
type Conn struct {
	rw net.Conn
	br *bufio.Reader
}

// Upgrade performs the WebSocket handshake on an incoming HTTP request.
// On success, the HTTP connection is hijacked (taken over from net/http's
// normal request/response cycle) and returned as a *Conn for framed I/O.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Upgrade") != "websocket" {
		return nil, errors.New("wsserver: not a websocket upgrade request")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("wsserver: ResponseWriter does not support hijacking")
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("wsserver: hijack failed: %w", err)
	}

	accept := computeAccept(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("wsserver: writing handshake response: %w", err)
	}

	return &Conn{rw: conn, br: buf.Reader}, nil
}

func computeAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsMagicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// WriteMessage sends a single unfragmented text frame. Server->client
// frames are never masked (only client->server frames are, per spec).
func (c *Conn) WriteMessage(payload []byte) error {
	return c.writeFrame(opText, payload)
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	var header []byte
	firstByte := byte(0x80) | opcode // FIN=1, opcode

	n := len(payload)
	switch {
	case n <= 125:
		header = []byte{firstByte, byte(n)}
	case n <= 65535:
		header = make([]byte, 4)
		header[0] = firstByte
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:], uint16(n))
	default:
		header = make([]byte, 10)
		header[0] = firstByte
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:], uint64(n))
	}

	if _, err := c.rw.Write(header); err != nil {
		return err
	}
	_, err := c.rw.Write(payload)
	return err
}

// Close sends a close frame and closes the underlying connection.
func (c *Conn) Close() error {
	_ = c.writeFrame(opClose, nil)
	return c.rw.Close()
}

// ReadMessage blocks until a text frame arrives and returns its payload.
// Client frames are always masked (RFC 6455 §5.3) — this unmasks them.
// Ping frames are answered with pong automatically and not returned to
// the caller; close frames return io.EOF.
func (c *Conn) ReadMessage() ([]byte, error) {
	for {
		opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case opText:
			return payload, nil
		case opClose:
			return nil, io.EOF
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
			// loop again for the next real frame
		case opPong:
			// ignore, loop again
		default:
			// unsupported opcode (binary, continuation): ignore and loop
		}
	}
}

func (c *Conn) readFrame() (opcode byte, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(c.br, head); err != nil {
		return 0, nil, err
	}
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7F)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.br, ext); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext))
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return opcode, payload, nil
}
