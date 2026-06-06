package metrics

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

var (
	EmailsReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orvix_emails_received_total",
			Help: "Total number of emails received.",
		},
		[]string{"domain", "status"},
	)

	EmailsSent = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orvix_emails_sent_total",
			Help: "Total number of emails sent.",
		},
		[]string{"domain", "status"},
	)

	QueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "orvix_queue_depth",
			Help: "Current mail queue depth.",
		},
	)

	ActiveConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "orvix_active_connections",
			Help: "Active mail protocol connections.",
		},
		[]string{"protocol"},
	)

	HTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orvix_http_requests_total",
			Help: "Total HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)

	HTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "orvix_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	LicenseExpiry = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "orvix_license_expiry_days",
			Help: "Days until license expires.",
		},
	)

	ModuleInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "orvix_module_info",
			Help: "Module version information.",
		},
		[]string{"module_id", "version", "status"},
	)

	FirewallBlocks = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "orvix_firewall_blocks_total",
			Help: "Total number of emails blocked by firewall.",
		},
	)

	SpamScore = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "orvix_spam_score",
			Help:    "Spam score distribution.",
			Buckets: []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
	)

	HealActions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "orvix_heal_actions_total",
			Help: "Total auto-heal actions.",
		},
		[]string{"check_name", "success"},
	)
)

func init() {
	prometheus.MustRegister(EmailsReceived)
	prometheus.MustRegister(EmailsSent)
	prometheus.MustRegister(QueueDepth)
	prometheus.MustRegister(ActiveConnections)
	prometheus.MustRegister(HTTPRequests)
	prometheus.MustRegister(HTTPDuration)
	prometheus.MustRegister(LicenseExpiry)
	prometheus.MustRegister(ModuleInfo)
	prometheus.MustRegister(FirewallBlocks)
	prometheus.MustRegister(SpamScore)
	prometheus.MustRegister(HealActions)
}

// Handler returns a Fiber handler for Prometheus metrics.
func Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		gatherer := prometheus.DefaultGatherer
		mfs, err := gatherer.Gather()
		if err != nil {
			c.Set("Content-Type", "text/plain; version=0.0.4")
			return c.Status(500).SendString("metrics gather error: " + err.Error())
		}

		var sb strings.Builder
		for _, mf := range mfs {
			_, err := expfmt.MetricFamilyToText(&sb, mf)
			if err != nil {
				continue
			}
		}

		c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(sb.String())
	}
}

// Ensure dto is used
var _ = dto.MetricFamily{}
