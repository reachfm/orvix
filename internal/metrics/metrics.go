package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Service struct {
	registry *prometheus.Registry
}

func NewService() *Service {
	return &Service{
		registry: prometheus.NewRegistry(),
	}
}

// Package-level metric descriptors (created once, used by router middleware)
var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orvix_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "orvix_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	ActiveConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orvix_active_connections",
		Help: "Current number of active connections",
	})

	LicenseInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "orvix_license_info",
			Help: "License information",
		},
		[]string{"tier", "status"},
	)

	MailSentTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orvix_mail_sent_total",
		Help: "Total number of emails sent",
	})

	MailReceivedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orvix_mail_received_total",
		Help: "Total number of emails received",
	})

	QueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orvix_queue_depth",
		Help: "Current mail queue depth",
	})

	HealthChecksTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orvix_health_checks_total",
		Help: "Total health checks performed",
	})

	DBConnectionPool = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orvix_db_connection_pool",
		Help: "Database connection pool size",
	})

	APILatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "orvix_api_latency_seconds",
		Help:    "API endpoint latency",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	})
)

func init() {
	// Pre-register with default registry so promhttp.Handler() works
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		ActiveConnections,
		LicenseInfo,
		MailSentTotal,
		MailReceivedTotal,
		QueueDepth,
		HealthChecksTotal,
		DBConnectionPool,
		APILatency,
	)
}

// Register is idempotent — metrics are already registered via init().
// Kept for API compatibility. Does nothing on subsequent calls.
var registered bool

func (s *Service) Register() {
	if registered {
		return
	}
	registered = true
}

func (s *Service) Handler() http.Handler {
	return promhttp.Handler()
}
