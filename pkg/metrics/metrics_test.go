package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestMetricsHandlerAndMethods(t *testing.T) {
	metrics := New()

	metrics.IncHTTPInFlight(http.MethodGet)
	metrics.DecHTTPInFlight(http.MethodGet)
	metrics.ObserveHTTPRequest(http.MethodGet, "/livez", http.StatusOK, 10*time.Millisecond)
	metrics.IncConsumerReconnect("connect_error")
	metrics.SetConsumerConnected(true)
	metrics.IncDLQMessage("max_retries")
	metrics.IncOutboxPublish("error")

	if got := metricValue(t, metrics, "http_requests_total", map[string]string{
		"method": http.MethodGet,
		"route":  "/livez",
		"status": "200",
	}); got != 1 {
		t.Fatalf("expected HTTP request counter 1, got %v", got)
	}
	if got := metricValue(t, metrics, "feed_consumer_reconnect_total", map[string]string{
		"result": "connect_error",
	}); got != 1 {
		t.Fatalf("expected reconnect counter 1, got %v", got)
	}
	if got := metricValue(t, metrics, "feed_consumer_connected", nil); got != 1 {
		t.Fatalf("expected connected gauge 1, got %v", got)
	}
	if got := metricValue(t, metrics, "feed_consumer_dlq_total", map[string]string{
		"reason": "max_retries",
	}); got != 1 {
		t.Fatalf("expected DLQ counter 1, got %v", got)
	}
	if got := metricValue(t, metrics, "outbox_publish_total", map[string]string{
		"result": "error",
	}); got != 1 {
		t.Fatalf("expected outbox publish counter 1, got %v", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "http_requests_total") {
		t.Fatalf("expected metrics body to contain http_requests_total")
	}
}

func metricValue(
	t *testing.T,
	metrics *Metrics,
	name string,
	wantLabels map[string]string,
) float64 {
	t.Helper()

	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !labelsMatch(metric.GetLabel(), wantLabels) {
				continue
			}
			if metric.Counter != nil {
				return metric.Counter.GetValue()
			}
			if metric.Gauge != nil {
				return metric.Gauge.GetValue()
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, wantLabels)
	return 0
}

func labelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	got := make(map[string]string, len(labels))
	for _, label := range labels {
		got[label.GetName()] = label.GetValue()
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
