package prommetrics

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	apiCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_count",
			Help: "Total number of API calls",
		},
		[]string{"module", "path", "method", "code"},
	)
	apiDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_request_duration_seconds",
			Help:    "API request latency in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"module", "path", "method", "code"},
	)
	rpcCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rpc_count",
			Help: "Total number of RPC calls",
		},
		[]string{"name", "path", "code"},
	)
	rpcDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rpc_request_duration_seconds",
			Help:    "RPC request latency in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"name", "path", "code"},
	)
)

func newRegistry(cs ...prometheus.Collector) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	for _, c := range cs {
		reg.MustRegister(c)
	}
	return reg
}

func StartAPIServer(listener net.Listener) error {
	reg := newRegistry(apiCounter, apiDuration)
	srv := &http.Server{Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})}
	go func() {
		_ = srv.Serve(listener)
	}()
	return nil
}

func StartRPCServer(listener net.Listener) error {
	reg := newRegistry(rpcCounter, rpcDuration)
	srv := &http.Server{Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})}
	go func() {
		_ = srv.Serve(listener)
	}()
	return nil
}

func APIMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := metricPath(c.FullPath(), c.Request.URL.Path, c.Writer.Status())
		module := moduleFromPath(path)
		code := apiCode(c.Writer.Status())
		labels := []string{module, path, c.Request.Method, strconv.Itoa(code)}
		apiCounter.WithLabelValues(labels...).Inc()
		apiDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
	}
}

func RPCUnaryServerInterceptor(serviceName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := grpcCode(err)
		labels := []string{serviceName, info.FullMethod, strconv.Itoa(code)}
		rpcCounter.WithLabelValues(labels...).Inc()
		rpcDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

func moduleFromPath(path string) string {
	path = strings.TrimPrefix(path, "/")
	if path == "" || path == "<404>" || path == "<unmatched>" {
		return "unknown"
	}
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return "unknown"
}

func metricPath(fullPath, rawPath string, status int) string {
	if status == http.StatusNotFound {
		return "<404>"
	}
	if fullPath != "" && fullPath != "/" {
		return fullPath
	}
	_ = rawPath
	return "<unmatched>"
}

func apiCode(httpStatus int) int {
	if httpStatus >= 400 {
		return httpStatus
	}
	return 0
}

func grpcCode(err error) int {
	if err == nil {
		return int(codes.OK)
	}
	return int(status.Code(err))
}
