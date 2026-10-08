// cmd/loadtest — a minimal load-testing tool (spec §25).
//
// WHY hand-written instead of k6: k6 isn't a Go module (no `go get` would
// help) and isn't in Ubuntu's default apt repos — it needs Grafana's own
// apt repository, which this sandbox has no route to. Rather than hand
// you a k6 script I couldn't actually run, this is a small Go program
// doing exactly what the spec's load-test section asks for: fire a
// configurable number of concurrent "users" at an endpoint, for a fixed
// duration, and report real requests/sec, latency percentiles, and error
// rate — computed from real HTTP round trips, not estimated.
//
// Your machine has full internet, so installing the real k6
// (https://k6.io) and translating this into a .js script is a reasonable
// upgrade whenever you want it — the methodology (concurrency levels,
// duration-based runs, percentile reporting) carries over directly.
//
// Run:
//
//	go run ./tools/loadtest --target http://localhost:8080 --concurrency 10,50,100 --duration 10s
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	latencies   []time.Duration // guarded by mu
	mu          sync.Mutex
	totalOK     int64 // atomic
	totalErr    int64 // atomic
	statusCount sync.Map
	firstErr    string // first connection-level error seen, guarded by mu
}

// recordErr keeps only the FIRST connection-level error message — enough
// to tell "server not running" from "timeout" without flooding output.
func (r *result) recordErr(err error) {
	r.mu.Lock()
	if r.firstErr == "" {
		r.firstErr = err.Error()
	}
	r.mu.Unlock()
}

func (r *result) record(status int, d time.Duration, ok bool) {
	r.mu.Lock()
	r.latencies = append(r.latencies, d)
	r.mu.Unlock()

	if ok {
		atomic.AddInt64(&r.totalOK, 1)
	} else {
		atomic.AddInt64(&r.totalErr, 1)
	}
	v, _ := r.statusCount.LoadOrStore(status, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

func (r *result) percentile(p float64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(r.latencies))
	copy(sorted, r.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func main() {
	target := flag.String("target", "http://localhost:8080", "base URL of the Go backend")
	concurrencyList := flag.String("concurrency", "10,50,100", "comma-separated list of concurrent-user levels to test, one run per level")
	duration := flag.Duration("duration", 10*time.Second, "how long to run at each concurrency level")
	endpoint := flag.String("endpoint", "/api/v1/predict", "endpoint to load test")
	uniquePayloads := flag.Bool("unique-payloads", true, "vary pickup coords per request (mostly cache misses, stresses the full path) vs a fixed payload (mostly cache hits)")
	flag.Parse()

	levels, err := parseLevels(*concurrencyList)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad --concurrency:", err)
		os.Exit(1)
	}

	fmt.Printf("Load testing %s%s — levels=%v duration=%s unique_payloads=%v\n\n",
		*target, *endpoint, levels, *duration, *uniquePayloads)
	fmt.Println("concurrency | requests | ok | errors | req/s | p50 | p95 | p99 | max")
	fmt.Println(strings.Repeat("-", 95))

	for _, c := range levels {
		r := runLevel(*target, *endpoint, c, *duration, *uniquePayloads)
		total := atomic.LoadInt64(&r.totalOK) + atomic.LoadInt64(&r.totalErr)
		reqPerSec := float64(total) / duration.Seconds()
		fmt.Printf("%-11d | %-8d | %-6d | %-6d | %-6.1f | %-6s | %-6s | %-6s | %s\n",
			c, total, r.totalOK, r.totalErr, reqPerSec,
			fmtMs(r.percentile(0.50)), fmtMs(r.percentile(0.95)), fmtMs(r.percentile(0.99)), fmtMs(r.percentile(1.0)))

		// status code breakdown, useful for spotting 503s (queue full) vs 502s (ml-service errors)
		var codes []string
		r.statusCount.Range(func(k, v any) bool {
			codes = append(codes, fmt.Sprintf("%d=%d", k, atomic.LoadInt64(v.(*int64))))
			return true
		})
		sort.Strings(codes)
		fmt.Printf("              status codes: %s\n", strings.Join(codes, " "))
		if r.firstErr != "" {
			fmt.Printf("              first connection error: %s\n", r.firstErr)
		}
		if v, ok := r.statusCount.Load(429); ok && atomic.LoadInt64(v.(*int64)) > 0 {
			fmt.Println("              note: 429s mean the server's rate limiter is active; start the server with RATE_LIMIT_PER_MINUTE=1000000 for benchmarks")
		}
		fmt.Println()
	}
}

func parseLevels(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func fmtMs(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// runLevel fires `concurrency` goroutines, each looping (fire request,
// wait for response, fire again) for `dur`, against target+endpoint.
// This models "N concurrent users each hammering the API as fast as they
// can" — the standard load-test shape, same as k6's constant-vus executor.
func runLevel(target, endpoint string, concurrency int, dur time.Duration, uniquePayloads bool) *result {
	r := &result{}
	// Go's default Transport keeps only 2 idle connections per host, so
	// with 150+ concurrent goroutines most requests would open a brand-new
	// TCP connection each time — burning client CPU on handshakes and
	// piling up TIME_WAIT sockets (on Windows that can exhaust ephemeral
	// ports and show up as bogus status-0 connection errors). Sizing the
	// idle pool to the concurrency level makes each "user" reuse one
	// connection, like a real keep-alive client (and like k6 does).
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency,
			MaxIdleConnsPerHost: concurrency,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(dur)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(workerID) + time.Now().UnixNano()))
			for time.Now().Before(deadline) {
				body := payload(rng, uniquePayloads)
				start := time.Now()
				status, err := doRequest(client, target+endpoint, body)
				elapsed := time.Since(start)
				ok := err == nil && status >= 200 && status < 300
				if err != nil {
					r.recordErr(err)
					status = 0 // connection-level failure, no HTTP status
				}
				r.record(status, elapsed, ok)
			}
		}(i)
	}
	wg.Wait()
	return r
}

// NYC bounding box, same as the backend's own validation (handlers.go) —
// kept well inside it (±0.05) so randomized requests never trip 400s for
// out-of-bounds coordinates, which would pollute the latency/error
// numbers with validation rejects instead of real ML-service load.
func payload(rng *rand.Rand, unique bool) []byte {
	lat, lon := 40.75, -73.98
	dlat, dlon := 40.73, -73.99
	if unique {
		lat += rng.Float64() * 0.05
		lon -= rng.Float64() * 0.05
		dlat += rng.Float64() * 0.05
		dlon -= rng.Float64() * 0.05
	}
	body, _ := json.Marshal(map[string]any{
		"pickup_latitude":   lat,
		"pickup_longitude":  lon,
		"dropoff_latitude":  dlat,
		"dropoff_longitude": dlon,
		"passenger_count":   1 + rng.Intn(4),
		"pickup_datetime":   "2016-03-15T18:30:00",
	})
	return body
}

func doRequest(client *http.Client, url string, body []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	return resp.StatusCode, nil
}