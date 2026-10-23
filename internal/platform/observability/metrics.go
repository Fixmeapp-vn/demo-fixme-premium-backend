// Package observability provides Prometheus metrics for the application.
package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// OtherMethod labels requests using a non-standard HTTP method, keeping
// the method label bounded.
const OtherMethod = "OTHER"

var knownMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {}, http.MethodPut: {},
	http.MethodPatch: {}, http.MethodDelete: {}, http.MethodConnect: {},
	http.MethodOptions: {}, http.MethodTrace: {},
}

// Metrics owns a private registry holding HTTP and Go runtime/process metrics.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics creates a registry with HTTP request metrics and the standard
// Go runtime and process collectors.
func NewMetrics() *Metrics {
	labels := []string{"method", "route", "status"}
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests handled, by method, route template and status.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by method, route template and status.",
			Buckets: prometheus.DefBuckets,
		}, labels),
	}
	m.registry.MustRegister(
		m.requests,
		m.duration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// ObserveHTTPRequest records one completed request. route must be a route
// template or another bounded value, never a raw request path.
func (m *Metrics) ObserveHTTPRequest(method, route string, status int, d time.Duration) {
	if _, ok := knownMethods[method]; !ok {
		method = OtherMethod
	}
	s := strconv.Itoa(status)
	m.requests.WithLabelValues(method, route, s).Inc()
	m.duration.WithLabelValues(method, route, s).Observe(d.Seconds())
}

// Registry exposes the underlying registry, e.g. for tests.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler returns the internal router serving GET /metrics. It must only be
// mounted on the internal metrics listener, never on the public API router.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}
