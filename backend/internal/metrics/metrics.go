// Package metrics implements just enough of the Prometheus client
// library to expose real metrics from a hand-rolled /api/v1/metrics
// endpoint — no external dependency (prometheus/client_golang), same
// constraint as pgx/go-redis/gorilla-websocket in earlier phases: no
// route to proxy.golang.org in this sandbox to fetch it.
//
// The text exposition format this package writes is the real format
// (https://prometheus.io/docs/instrumenting/exposition_formats/) — it
// was verified against a real `prometheus` binary (installed via apt in
// this sandbox) actually scraping it successfully, not just eyeballed
// against the docs.
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ---------- Counter ----------

// Counter is a monotonically increasing value (requests served, errors,
// cache hits...). atomic.Int64 under the hood — safe for concurrent Inc()
// from every worker/handler goroutine without a lock.
type Counter struct {
	value int64
}

func (c *Counter) Inc()         { atomic.AddInt64(&c.value, 1) }
func (c *Counter) Add(n int64)  { atomic.AddInt64(&c.value, n) }
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.value) }

// ---------- Gauge ----------

// Gauge is a value that can go up or down (queue length, busy workers).
// Stored as float64 bits so Set() can take fractional values too
// (useful for ratios like cache hit rate, even though this app's gauges
// are currently all integers).
type Gauge struct {
	bits uint64
	fn   func() float64 // if set, Value() calls this instead of reading bits
}

func (g *Gauge) Set(v float64) { atomic.StoreUint64(&g.bits, math.Float64bits(v)) }

// SetFunc makes this a "computed" gauge: its value is whatever fn()
// returns at scrape time, rather than something pushed via Set(). This
// is how queue_length/busy_workers/active_trips work — computed from the
// worker pool and trip repository's live state each time /metrics is
// scraped, rather than kept in sync via a background goroutine.
func (g *Gauge) SetFunc(fn func() float64) { g.fn = fn }

func (g *Gauge) Value() float64 {
	if g.fn != nil {
		return g.fn()
	}
	return math.Float64frombits(atomic.LoadUint64(&g.bits))
}

// ---------- labeled vectors ----------

// labeledSet is shared machinery for CounterVec/HistogramVec: a
// mutex-protected map from a label-value tuple (joined into one string
// key) to the underlying metric instance. Reads/writes to existing label
// combinations are still safe for high-frequency use because the
// Counter/Histogram values themselves are atomic — the mutex here only
// guards the map itself (looking up or creating a new label
// combination), not every Inc()/Observe().
type CounterVec struct {
	mu         sync.Mutex
	labelNames []string
	children   map[string]*Counter
}

func NewCounterVec(labelNames ...string) *CounterVec {
	return &CounterVec{labelNames: labelNames, children: make(map[string]*Counter)}
}

func (cv *CounterVec) WithLabelValues(values ...string) *Counter {
	key := strings.Join(values, "\x00")
	cv.mu.Lock()
	c, ok := cv.children[key]
	if !ok {
		c = &Counter{}
		cv.children[key] = c
	}
	cv.mu.Unlock()
	return c
}

// ---------- Histogram ----------

// Histogram tracks observations (durations, in seconds) into cumulative
// buckets, matching Prometheus's own histogram semantics exactly: each
// bucket counts observations <= its upper bound ("le"), so bucket counts
// are cumulative and the last bucket (+Inf) always equals the total
// count. This is what lets PromQL compute percentiles later via
// histogram_quantile().
type Histogram struct {
	mu      sync.Mutex
	buckets []float64 // upper bounds, ascending
	counts  []int64   // counts[i] = observations with value <= buckets[i]
	sum     float64
	total   int64
}

// DefaultLatencyBuckets covers sub-millisecond cache hits up through
// multi-second worst-case ML-service calls under load.
var DefaultLatencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

func NewHistogram(buckets []float64) *Histogram {
	b := make([]float64, len(buckets))
	copy(b, buckets)
	sort.Float64s(b)
	return &Histogram{buckets: b, counts: make([]int64, len(b))}
}

func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, bound := range h.buckets {
		if v <= bound {
			h.counts[i]++
		}
	}
	h.sum += v
	h.total++
}

type HistogramVec struct {
	mu         sync.Mutex
	labelNames []string
	buckets    []float64
	children   map[string]*Histogram
}

func NewHistogramVec(buckets []float64, labelNames ...string) *HistogramVec {
	return &HistogramVec{labelNames: labelNames, buckets: buckets, children: make(map[string]*Histogram)}
}

func (hv *HistogramVec) WithLabelValues(values ...string) *Histogram {
	key := strings.Join(values, "\x00")
	hv.mu.Lock()
	h, ok := hv.children[key]
	if !ok {
		h = NewHistogram(hv.buckets)
		hv.children[key] = h
	}
	hv.mu.Unlock()
	return h
}

// ---------- registry + exposition format ----------

type metricEntry struct {
	name string
	help string
	typ  string // "counter" | "gauge" | "histogram"
	m    any
}

type Registry struct {
	mu      sync.Mutex
	entries []metricEntry
}

var Default = NewRegistry()

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) registerCounter(name, help string, c *Counter) *Counter {
	r.mu.Lock()
	r.entries = append(r.entries, metricEntry{name, help, "counter", c})
	r.mu.Unlock()
	return c
}

func (r *Registry) registerCounterVec(name, help string, cv *CounterVec) *CounterVec {
	r.mu.Lock()
	r.entries = append(r.entries, metricEntry{name, help, "counter", cv})
	r.mu.Unlock()
	return cv
}

func (r *Registry) registerGauge(name, help string, g *Gauge) *Gauge {
	r.mu.Lock()
	r.entries = append(r.entries, metricEntry{name, help, "gauge", g})
	r.mu.Unlock()
	return g
}

func (r *Registry) registerHistogram(name, help string, h *Histogram) *Histogram {
	r.mu.Lock()
	r.entries = append(r.entries, metricEntry{name, help, "histogram", h})
	r.mu.Unlock()
	return h
}

func (r *Registry) registerHistogramVec(name, help string, hv *HistogramVec) *HistogramVec {
	r.mu.Lock()
	r.entries = append(r.entries, metricEntry{name, help, "histogram", hv})
	r.mu.Unlock()
	return hv
}

func NewCounter(name, help string) *Counter { return Default.registerCounter(name, help, &Counter{}) }
func NewRegisteredCounterVec(name, help string, labelNames ...string) *CounterVec {
	return Default.registerCounterVec(name, help, NewCounterVec(labelNames...))
}
func NewRegisteredGauge(name, help string) *Gauge { return Default.registerGauge(name, help, &Gauge{}) }
func NewRegisteredHistogram(name, help string, buckets []float64) *Histogram {
	return Default.registerHistogram(name, help, NewHistogram(buckets))
}
func NewRegisteredHistogramVec(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	return Default.registerHistogramVec(name, help, NewHistogramVec(buckets, labelNames...))
}

// WriteTo renders every registered metric in Prometheus text exposition
// format. Order follows registration order — Prometheus doesn't require
// any particular order, this just makes output stable/readable.
func (r *Registry) Gather(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, e := range r.entries {
		fmt.Fprintf(w, "# HELP %s %s\n", e.name, e.help)
		fmt.Fprintf(w, "# TYPE %s %s\n", e.name, e.typ)

		switch m := e.m.(type) {
		case *Counter:
			fmt.Fprintf(w, "%s %v\n", e.name, m.Value())

		case *CounterVec:
			m.mu.Lock()
			for key, c := range m.children {
				fmt.Fprintf(w, "%s%s %v\n", e.name, labelString(m.labelNames, key), c.Value())
			}
			m.mu.Unlock()

		case *Gauge:
			fmt.Fprintf(w, "%s %v\n", e.name, m.Value())

		case *Histogram:
			writeHistogram(w, e.name, "", m)

		case *HistogramVec:
			m.mu.Lock()
			for key, h := range m.children {
				writeHistogram(w, e.name, labelString(m.labelNames, key), h)
			}
			m.mu.Unlock()
		}
	}
}

func writeHistogram(w io.Writer, name, labels string, h *Histogram) {
	h.mu.Lock()
	defer h.mu.Unlock()

	base := name
	if labels == "" {
		for i, bound := range h.buckets {
			fmt.Fprintf(w, "%s_bucket{le=\"%s\"} %d\n", base, formatBound(bound), h.counts[i])
		}
		fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", base, h.total)
		fmt.Fprintf(w, "%s_sum %v\n", base, h.sum)
		fmt.Fprintf(w, "%s_count %d\n", base, h.total)
		return
	}
	// labels is already "{k=\"v\",...}" — splice le= in before the closing brace
	inner := strings.TrimSuffix(strings.TrimPrefix(labels, "{"), "}")
	for i, bound := range h.buckets {
		fmt.Fprintf(w, "%s_bucket{%s,le=\"%s\"} %d\n", base, inner, formatBound(bound), h.counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{%s,le=\"+Inf\"} %d\n", base, inner, h.total)
	fmt.Fprintf(w, "%s_sum%s %v\n", base, labels, h.sum)
	fmt.Fprintf(w, "%s_count%s %d\n", base, labels, h.total)
}

func formatBound(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", f), "0"), ".")
}

// labelString reconstructs "{name1=\"v1\",name2=\"v2\"}" from the vector's
// label names and a child's map key (values joined with \x00).
func labelString(labelNames []string, key string) string {
	values := strings.Split(key, "\x00")
	parts := make([]string, len(labelNames))
	for i, n := range labelNames {
		v := ""
		if i < len(values) {
			v = values[i]
		}
		parts[i] = fmt.Sprintf("%s=%q", n, v)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
