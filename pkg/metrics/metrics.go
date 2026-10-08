package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 持有应用级 Prometheus 指标和独立 Registry。
//
// 使用独立 Registry 而不是 prometheus.DefaultRegisterer，可以避免测试之间
// 重复注册指标，也让依赖关系更明确。
type Metrics struct {
	registry *prometheus.Registry

	httpInFlight *prometheus.GaugeVec
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	consumerReconnect *prometheus.CounterVec
	consumerConnected prometheus.Gauge
	consumerDLQ       *prometheus.CounterVec

	outboxPublish *prometheus.CounterVec
}

func New() *Metrics {
	registry := prometheus.NewRegistry()

	httpInFlight := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_in_flight_requests",
			Help: "Current number of HTTP requests being processed.",
		},
		[]string{"method"},
	)
	httpRequests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of completed HTTP requests.",
		},
		[]string{"method", "route", "status"},
	)
	httpDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request processing duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	consumerReconnect := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feed_consumer_reconnect_total",
			Help: "Total number of Feed consumer reconnect events.",
		},
		[]string{"result"},
	)
	consumerConnected := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "feed_consumer_connected",
			Help: "Whether the Feed consumer currently has an active session.",
		},
	)
	consumerDLQ := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feed_consumer_dlq_total",
			Help: "Total number of Feed messages sent to the dead-letter queue.",
		},
		[]string{"reason"},
	)
	outboxPublish := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "outbox_publish_total",
			Help: "Total number of Outbox publish attempts by result.",
		},
		[]string{"result"},
	)

	registry.MustRegister(
		httpInFlight,
		httpRequests,
		httpDuration,
		consumerReconnect,
		consumerConnected,
		consumerDLQ,
		outboxPublish,
	)
	consumerConnected.Set(0)

	return &Metrics{
		registry:          registry,
		httpInFlight:      httpInFlight,
		httpRequests:      httpRequests,
		httpDuration:      httpDuration,
		consumerReconnect: consumerReconnect,
		consumerConnected: consumerConnected,
		consumerDLQ:       consumerDLQ,
		outboxPublish:     outboxPublish,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) IncHTTPInFlight(method string) {
	m.httpInFlight.WithLabelValues(method).Inc()
}

func (m *Metrics) DecHTTPInFlight(method string) {
	m.httpInFlight.WithLabelValues(method).Dec()
}

func (m *Metrics) ObserveHTTPRequest(
	method string,
	route string,
	status int,
	duration time.Duration,
) {
	statusLabel := strconv.Itoa(status)
	m.httpRequests.WithLabelValues(method, route, statusLabel).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (m *Metrics) IncConsumerReconnect(result string) {
	m.consumerReconnect.WithLabelValues(result).Inc()
}

func (m *Metrics) SetConsumerConnected(connected bool) {
	if connected {
		m.consumerConnected.Set(1)
		return
	}
	m.consumerConnected.Set(0)
}

func (m *Metrics) IncDLQMessage(reason string) {
	m.consumerDLQ.WithLabelValues(reason).Inc()
}

func (m *Metrics) IncOutboxPublish(result string) {
	m.outboxPublish.WithLabelValues(result).Inc()
}
