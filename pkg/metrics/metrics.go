// Package metrics exposes Prometheus metrics: RED metrics for HTTP (Fiber)
// and gRPC servers, plus a registry for service-specific business
// metrics. Scraped at /metrics, which is only reachable on the private
// network (the web apps' nginx proxies /api/* only).
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Registry is the service's metric registry (Go runtime and process
// metrics included).
var Registry = func() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}()

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by method, route template and status code.",
	}, []string{"method", "route", "status"})

	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by method and route template.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "route"})

	grpcRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "grpc_server_handled_total",
		Help: "gRPC calls by method and status code.",
	}, []string{"method", "code"})

	grpcDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_server_handling_seconds",
		Help:    "gRPC call latency by method.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"method"})

	// KafkaMessages counts consumed messages by topic and result
	// (ok | failed).
	KafkaMessages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kafka_messages_consumed_total",
		Help: "Kafka messages handled by topic and result.",
	}, []string{"topic", "result"})
)

func init() {
	Registry.MustRegister(httpRequests, httpDuration, grpcRequests, grpcDuration, KafkaMessages)
}

// Fiber records RED metrics per route template (e.g. /api/v1/orders/:id),
// never per raw path, so cardinality stays bounded.
func Fiber() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		// Copy: fasthttp reuses the request buffer these strings point
		// into, and Prometheus keeps label values forever.
		method := strings.Clone(c.Method())
		route := strings.Clone(c.Route().Path)
		if route == "" || route == "/" {
			route = "unmatched"
		}
		code := c.Response().StatusCode()
		if err != nil {
			if fe, ok := err.(*fiber.Error); ok {
				code = fe.Code
			} else {
				code = fiber.StatusInternalServerError
			}
		}
		httpRequests.WithLabelValues(method, route, strconv.Itoa(code)).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		return err
	}
}

// UnaryServerInterceptor records RED metrics for gRPC servers.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		grpcRequests.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
		grpcDuration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

// Handler serves the registry in the Prometheus text format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}
