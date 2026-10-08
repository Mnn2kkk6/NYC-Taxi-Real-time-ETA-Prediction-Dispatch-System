// Package redisclient is a minimal Redis client speaking RESP (REdis
// Serialization Protocol) directly over net.Conn — no external driver.
//
// WHY hand-write this instead of using github.com/redis/go-redis/v9: this
// sandbox has no network route to proxy.golang.org, so no third-party Go
// module can be fetched and verified here (same constraint that applies
// to pgx in Phase 3/5's Postgres work). Your machine has full internet —
// swapping this for go-redis is a reasonable thing to do for a "real"
// production system, and this file's public API (Get/SetEX/Incr/Expire/
// Ping) is intentionally the same shape so that swap touches only this
// one file. Until then, this version is genuinely useful: it was written
// against and tested with a real redis-server instance, not mocked.
//
// RESP basics implemented here (protocol version 2, sufficient for the
// commands we need):
//   - Requests are always sent as an array of bulk strings:
//     *<argc>\r\n$<len>\r\n<arg>\r\n ...
//   - Replies come back as one of: Simple String (+OK\r\n),
//     Error (-ERR ...\r\n), Integer (:123\r\n),
//     Bulk String ($5\r\nhello\r\n, or $-1\r\n for nil/key-not-found),
//     Array (*N\r\n...) — not needed for our command set but parsed for completeness.
package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

var ErrNil = errors.New("redis: nil (key not found)")

type Client struct {
	addr     string
	pool     chan net.Conn
	poolSize int
	dialTO   time.Duration
}

func New(addr string, poolSize int) *Client {
	return &Client{
		addr:     addr,
		pool:     make(chan net.Conn, poolSize),
		poolSize: poolSize,
		dialTO:   2 * time.Second,
	}
}

func (c *Client) getConn(ctx context.Context) (net.Conn, error) {
	select {
	case conn := <-c.pool:
		return conn, nil
	default:
	}
	d := net.Dialer{Timeout: c.dialTO}
	return d.DialContext(ctx, "tcp", c.addr)
}

// putConn returns a healthy connection to the pool; if the pool is full
// or the connection is unusable, it's closed instead of leaked.
func (c *Client) putConn(conn net.Conn, healthy bool) {
	if !healthy {
		conn.Close()
		return
	}
	select {
	case c.pool <- conn:
	default:
		conn.Close()
	}
}

// do sends a RESP command and returns the raw reply string plus its type
// byte ('+' simple, '-' error, ':' integer, '$' bulk, '*' array).
func (c *Client) do(ctx context.Context, args ...string) (byte, string, error) {
	conn, err := c.getConn(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("redis: connect: %w", err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Second))
	}

	if err := writeCommand(conn, args); err != nil {
		c.putConn(conn, false)
		return 0, "", fmt.Errorf("redis: write: %w", err)
	}

	typ, val, err := readReply(bufio.NewReader(conn))
	if err != nil {
		c.putConn(conn, false)
		return 0, "", fmt.Errorf("redis: read: %w", err)
	}
	c.putConn(conn, true)

	if typ == '-' {
		return typ, val, fmt.Errorf("redis: server error: %s", val)
	}
	return typ, val, nil
}

func writeCommand(w net.Conn, args []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	_, err := w.Write([]byte(b.String()))
	return err
}

func readReply(r *bufio.Reader) (byte, string, error) {
	line, err := readLine(r)
	if err != nil {
		return 0, "", err
	}
	if len(line) == 0 {
		return 0, "", errors.New("empty reply")
	}
	typ := line[0]
	body := line[1:]

	switch typ {
	case '+', '-', ':':
		return typ, body, nil
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil {
			return 0, "", fmt.Errorf("bad bulk length: %w", err)
		}
		if n == -1 {
			return '$', "", nil // nil bulk string (key not found)
		}
		buf := make([]byte, n+2) // +2 for trailing \r\n
		if _, err := readFull(r, buf); err != nil {
			return 0, "", err
		}
		return '$', string(buf[:n]), nil
	case '*':
		// Array reply: not needed for GET/SETEX/INCR/EXPIRE/PING, but
		// drain it correctly so the connection stays in sync for reuse.
		n, err := strconv.Atoi(body)
		if err != nil || n <= 0 {
			return '*', "", nil
		}
		var last string
		for i := 0; i < n; i++ {
			_, v, err := readReply(r)
			if err != nil {
				return 0, "", err
			}
			last = v
		}
		return '*', last, nil
	default:
		return 0, "", fmt.Errorf("unknown reply type byte: %q", typ)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// --- public command API ---

func (c *Client) Ping(ctx context.Context) error {
	_, _, err := c.do(ctx, "PING")
	return err
}

// GetOrNil returns (value, found, error) — found=false means a real
// cache miss (key doesn't exist), not an infrastructure failure.
func (c *Client) GetOrNil(ctx context.Context, key string) (string, bool, error) {
	conn, err := c.getConn(ctx)
	if err != nil {
		return "", false, fmt.Errorf("redis: connect: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Second))
	}
	if err := writeCommand(conn, []string{"GET", key}); err != nil {
		c.putConn(conn, false)
		return "", false, fmt.Errorf("redis: write: %w", err)
	}
	r := bufio.NewReader(conn)
	line, err := readLine(r)
	if err != nil {
		c.putConn(conn, false)
		return "", false, fmt.Errorf("redis: read: %w", err)
	}
	if len(line) == 0 || line[0] != '$' {
		c.putConn(conn, true)
		return "", false, fmt.Errorf("redis: unexpected reply: %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		c.putConn(conn, false)
		return "", false, fmt.Errorf("bad bulk length: %w", err)
	}
	if n == -1 {
		c.putConn(conn, true)
		return "", false, nil // real miss
	}
	buf := make([]byte, n+2)
	if _, err := readFull(r, buf); err != nil {
		c.putConn(conn, false)
		return "", false, err
	}
	c.putConn(conn, true)
	return string(buf[:n]), true, nil
}

// SetEX sets key=value with a TTL in seconds — every cache write here has
// an expiry; nothing is cached forever.
func (c *Client) SetEX(ctx context.Context, key, value string, ttlSeconds int) error {
	_, _, err := c.do(ctx, "SETEX", key, strconv.Itoa(ttlSeconds), value)
	return err
}

// Incr atomically increments key (creating it at 1 if absent) and returns
// the new value — the building block for the fixed-window rate limiter.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	_, val, err := c.do(ctx, "INCR", key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("redis: unexpected INCR reply %q: %w", val, err)
	}
	return n, nil
}

func (c *Client) Expire(ctx context.Context, key string, ttlSeconds int) error {
	_, _, err := c.do(ctx, "EXPIRE", key, strconv.Itoa(ttlSeconds))
	return err
}

func (c *Client) Close() {
	close(c.pool)
	for conn := range c.pool {
		conn.Close()
	}
}
