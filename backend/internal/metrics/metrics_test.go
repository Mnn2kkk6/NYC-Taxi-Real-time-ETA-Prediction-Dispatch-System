package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestCounter_IncAndAdd(t *testing.T) {
	c := &Counter{}
	c.Inc()
	c.Inc()
	c.Add(5)
	if got := c.Value(); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestCounter_ConcurrentIncIsAtomic(t *testing.T) {
	c := &Counter{}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Inc()
		}()
	}
	wg.Wait()
	if got := c.Value(); got != 200 {
		t.Errorf("got %d, want 200 after 200 concurrent increments", got)
	}
}

func TestGauge_SetAndValue(t *testing.T) {
	g := &Gauge{}
	g.Set(3.5)
	if got := g.Value(); got != 3.5 {
		t.Errorf("got %v, want 3.5", got)
	}
	g.Set(-2)
	if got := g.Value(); got != -2 {
		t.Errorf("got %v, want -2 (gauges can decrease)", got)
	}
}

func TestGauge_SetFuncComputesLazily(t *testing.T) {
	calls := 0
	g := &Gauge{}
	g.SetFunc(func() float64 {
		calls++
		return float64(calls * 10)
	})
	if got := g.Value(); got != 10 {
		t.Errorf("first call: got %v, want 10", got)
	}
	if got := g.Value(); got != 20 {
		t.Errorf("second call: got %v, want 20 (SetFunc recomputes each time)", got)
	}
}

func TestCounterVec_SeparatesLabelCombinations(t *testing.T) {
	cv := NewCounterVec("route", "status")
	cv.WithLabelValues("predict", "200").Inc()
	cv.WithLabelValues("predict", "200").Inc()
	cv.WithLabelValues("predict", "503").Inc()
	cv.WithLabelValues("health", "200").Inc()

	if got := cv.WithLabelValues("predict", "200").Value(); got != 2 {
		t.Errorf("predict/200: got %d, want 2", got)
	}
	if got := cv.WithLabelValues("predict", "503").Value(); got != 1 {
		t.Errorf("predict/503: got %d, want 1", got)
	}
	if got := cv.WithLabelValues("health", "200").Value(); got != 1 {
		t.Errorf("health/200: got %d, want 1", got)
	}
}

func TestHistogram_BucketsAreCumulative(t *testing.T) {
	h := NewHistogram([]float64{0.1, 0.5, 1})
	h.Observe(0.05) // falls in every bucket
	h.Observe(0.3)  // falls in 0.5 and 1
	h.Observe(0.7)  // falls in 1 only
	h.Observe(5)    // falls in none of the finite buckets, only +Inf

	// bucket "le=0.1": only the 0.05 observation
	if h.counts[0] != 1 {
		t.Errorf("bucket le=0.1: got %d, want 1", h.counts[0])
	}
	// bucket "le=0.5": 0.05 and 0.3
	if h.counts[1] != 2 {
		t.Errorf("bucket le=0.5: got %d, want 2", h.counts[1])
	}
	// bucket "le=1": 0.05, 0.3, 0.7
	if h.counts[2] != 3 {
		t.Errorf("bucket le=1: got %d, want 3", h.counts[2])
	}
	if h.total != 4 {
		t.Errorf("total count: got %d, want 4 (+Inf bucket)", h.total)
	}
	wantSum := 0.05 + 0.3 + 0.7 + 5
	if h.sum != wantSum {
		t.Errorf("sum: got %v, want %v", h.sum, wantSum)
	}
}

func TestHistogram_SortsBucketsRegardlessOfInputOrder(t *testing.T) {
	h := NewHistogram([]float64{1, 0.1, 0.5}) // deliberately unsorted
	if h.buckets[0] != 0.1 || h.buckets[1] != 0.5 || h.buckets[2] != 1 {
		t.Errorf("expected buckets sorted ascending, got %v", h.buckets)
	}
}

func TestRegistry_GatherProducesValidExpositionFormat(t *testing.T) {
	r := NewRegistry()
	c := r.registerCounter("test_counter_total", "A test counter.", &Counter{})
	c.Add(42)

	g := r.registerGauge("test_gauge", "A test gauge.", &Gauge{})
	g.Set(7)

	var buf bytes.Buffer
	r.Gather(&buf)
	out := buf.String()

	wantLines := []string{
		"# HELP test_counter_total A test counter.",
		"# TYPE test_counter_total counter",
		"test_counter_total 42",
		"# HELP test_gauge A test gauge.",
		"# TYPE test_gauge gauge",
		"test_gauge 7",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRegistry_GatherCounterVecIncludesLabels(t *testing.T) {
	r := NewRegistry()
	cv := r.registerCounterVec("test_requests_total", "Test requests.", NewCounterVec("route", "status"))
	cv.WithLabelValues("predict", "200").Add(3)

	var buf bytes.Buffer
	r.Gather(&buf)
	out := buf.String()

	if !strings.Contains(out, `test_requests_total{route="predict",status="200"} 3`) {
		t.Errorf("expected labeled counter line, got:\n%s", out)
	}
}

func TestRegistry_GatherHistogramIncludesBucketsSumAndCount(t *testing.T) {
	r := NewRegistry()
	h := r.registerHistogram("test_latency_seconds", "Test latency.", NewHistogram([]float64{0.1, 1}))
	h.Observe(0.05)
	h.Observe(2)

	var buf bytes.Buffer
	r.Gather(&buf)
	out := buf.String()

	wantSubstrings := []string{
		`test_latency_seconds_bucket{le="0.1"} 1`,
		`test_latency_seconds_bucket{le="1"} 1`,
		`test_latency_seconds_bucket{le="+Inf"} 2`,
		"test_latency_seconds_sum 2.05",
		"test_latency_seconds_count 2",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestApp_MetricsAreRegisteredAndUsable(t *testing.T) {
	// App is the real, package-level registry used by the running server.
	// This just confirms it's wired up correctly (registered on Default,
	// nothing panics) — actual values are exercised by the handlers/pool
	// tests that call into these same metrics.
	App.HTTPRequestsTotal.WithLabelValues("test_route", "GET", "200").Inc()
	App.PredictionLatency.Observe(0.05)
	App.QueueLength.Set(3)

	var buf bytes.Buffer
	Default.Gather(&buf)
	out := buf.String()
	if !strings.Contains(out, "http_requests_total") {
		t.Error("expected http_requests_total to appear in the default registry's output")
	}
	if !strings.Contains(out, "queue_length 3") {
		t.Error("expected queue_length to reflect the value just Set()")
	}
}
