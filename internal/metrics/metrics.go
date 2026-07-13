// Package metrics holds the Prometheus collectors for the notifier. It is a
// singleton registered against the default registry so any package can
// record without threading a handle through.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// NotificationsDelivered counts successfully delivered notifications by
	// channel and ingest kind.
	NotificationsDelivered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_notifier_notifications_delivered_total",
		Help: "Notifications delivered to a Matrix room.",
	}, []string{"channel", "kind"})

	// NotificationsFailed counts notifications that could not be delivered.
	NotificationsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_notifier_notifications_failed_total",
		Help: "Notifications that failed to deliver.",
	}, []string{"channel", "kind"})

	// IngestRejected counts requests refused before delivery (auth, rate
	// limit, bad body).
	IngestRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_notifier_ingest_rejected_total",
		Help: "Ingest requests rejected before delivery.",
	}, []string{"kind", "reason"})

	// SendRetries counts individual send retry attempts.
	SendRetries = promauto.NewCounter(prometheus.CounterOpts{
		Name: "matrix_notifier_send_retries_total",
		Help: "Individual Matrix send retry attempts.",
	})

	// SendDuration measures end-to-end send latency (all retries included).
	SendDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "matrix_notifier_send_duration_seconds",
		Help:    "Time to deliver a message to Matrix, including retries.",
		Buckets: prometheus.DefBuckets,
	})

	// ChartRenders counts chart attachment attempts by outcome.
	ChartRenders = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "matrix_notifier_chart_renders_total",
		Help: "Prometheus chart render attempts.",
	}, []string{"outcome"})

	// ChartDuration measures chart query+render time.
	ChartDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "matrix_notifier_chart_duration_seconds",
		Help:    "Time to query Prometheus and render a chart.",
		Buckets: prometheus.DefBuckets,
	})

	// SyncAge is the seconds since the last successful Matrix sync. A rising
	// value means the bot is disconnected — alert on it.
	SyncAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_notifier_sync_age_seconds",
		Help: "Seconds since the last successful Matrix sync.",
	})

	// Verified reports whether the bot's device is cross-signed (1) or not.
	Verified = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "matrix_notifier_device_verified",
		Help: "1 if the bot device is cross-signed and verified, else 0.",
	})
)
