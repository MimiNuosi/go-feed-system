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

	httpInFlight    *prometheus.GaugeVec
	httpRequests    *prometheus.CounterVec
	httpDuration    *prometheus.HistogramVec
	httpRateLimited *prometheus.CounterVec

	consumerReconnect *prometheus.CounterVec
	consumerConnected prometheus.Gauge
	consumerDLQ       *prometheus.CounterVec

	outboxPublish *prometheus.CounterVec
	viewWriteback *prometheus.CounterVec

	realtimePublish    *prometheus.CounterVec
	realtimeReceive    *prometheus.CounterVec
	realtimeReconnect  *prometheus.CounterVec
	realtimeSubscriber prometheus.Gauge

	rateLimitBackend *prometheus.CounterVec

	backgroundWorkerLeader *prometheus.GaugeVec
	backgroundWorkerLease  *prometheus.CounterVec
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
	httpRateLimited := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_rate_limited_total",
			Help: "Total number of HTTP requests rejected by the rate limiter.",
		},
		[]string{"scope"},
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
	viewWriteback := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "view_writeback_total",
			Help: "Total number of view-count writeback attempts by result.",
		},
		[]string{"result"},
	)
	realtimePublish := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "realtime_publish_total",
			Help: "Total number of realtime publish attempts by channel and result.",
		},
		[]string{"channel", "result"},
	)
	realtimeReceive := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "realtime_receive_total",
			Help: "Total number of realtime receive attempts by channel and result.",
		},
		[]string{"channel", "result"},
	)
	realtimeReconnect := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "realtime_subscriber_reconnect_total",
			Help: "Total number of realtime subscriber reconnect events.",
		},
		[]string{"result"},
	)
	realtimeSubscriber := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "realtime_subscriber_connected",
			Help: "Whether the realtime Redis subscriber currently has an active subscription.",
		},
	)
	rateLimitBackend := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_backend_total",
			Help: "Total number of rate limit decisions by backend and result.",
		},
		[]string{"backend", "result"},
	)
	backgroundWorkerLeader := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "background_worker_leader",
			Help: "Whether this instance currently leads a background worker.",
		},
		[]string{"worker"},
	)
	backgroundWorkerLease := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "background_worker_lease_total",
			Help: "Total number of background worker lease events.",
		},
		[]string{"worker", "result"},
	)

	registry.MustRegister(
		httpInFlight,
		httpRequests,
		httpDuration,
		httpRateLimited,
		consumerReconnect,
		consumerConnected,
		consumerDLQ,
		outboxPublish,
		viewWriteback,
		realtimePublish,
		realtimeReceive,
		realtimeReconnect,
		realtimeSubscriber,
		rateLimitBackend,
		backgroundWorkerLeader,
		backgroundWorkerLease,
	)
	consumerConnected.Set(0)
	realtimeSubscriber.Set(0)

	return &Metrics{
		registry:               registry,
		httpInFlight:           httpInFlight,
		httpRequests:           httpRequests,
		httpDuration:           httpDuration,
		httpRateLimited:        httpRateLimited,
		consumerReconnect:      consumerReconnect,
		consumerConnected:      consumerConnected,
		consumerDLQ:            consumerDLQ,
		outboxPublish:          outboxPublish,
		viewWriteback:          viewWriteback,
		realtimePublish:        realtimePublish,
		realtimeReceive:        realtimeReceive,
		realtimeReconnect:      realtimeReconnect,
		realtimeSubscriber:     realtimeSubscriber,
		rateLimitBackend:       rateLimitBackend,
		backgroundWorkerLeader: backgroundWorkerLeader,
		backgroundWorkerLease:  backgroundWorkerLease,
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

func (m *Metrics) IncRateLimited(scope string) {
	m.httpRateLimited.WithLabelValues(scope).Inc()
}

func (m *Metrics) IncViewWriteback(result string) {
	m.viewWriteback.WithLabelValues(result).Inc()
}

func (m *Metrics) IncRealtimePublish(channel string, result string) {
	m.realtimePublish.WithLabelValues(channel, result).Inc()
}

func (m *Metrics) IncRealtimeReceive(channel string, result string) {
	m.realtimeReceive.WithLabelValues(channel, result).Inc()
}

func (m *Metrics) IncRealtimeReconnect(result string) {
	m.realtimeReconnect.WithLabelValues(result).Inc()
}

func (m *Metrics) SetRealtimeSubscriberConnected(connected bool) {
	if connected {
		m.realtimeSubscriber.Set(1)
		return
	}
	m.realtimeSubscriber.Set(0)
}

func (m *Metrics) IncRateLimitBackend(backend string, result string) {
	m.rateLimitBackend.WithLabelValues(backend, result).Inc()
}

func (m *Metrics) SetBackgroundWorkerLeader(worker string, leader bool) {
	value := 0.0
	if leader {
		value = 1
	}
	m.backgroundWorkerLeader.WithLabelValues(worker).Set(value)
}

func (m *Metrics) IncBackgroundWorkerLease(worker string, result string) {
	m.backgroundWorkerLease.WithLabelValues(worker, result).Inc()
}
