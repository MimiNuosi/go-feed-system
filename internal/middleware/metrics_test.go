package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type recordedHTTPObservation struct {
	method   string
	route    string
	status   int
	duration time.Duration
}

type fakeHTTPMetrics struct {
	inFlight     int
	observations []recordedHTTPObservation
}

func (f *fakeHTTPMetrics) IncHTTPInFlight(method string) {
	f.inFlight++
}

func (f *fakeHTTPMetrics) DecHTTPInFlight(method string) {
	f.inFlight--
}

func (f *fakeHTTPMetrics) ObserveHTTPRequest(
	method string,
	route string,
	status int,
	duration time.Duration,
) {
	f.observations = append(f.observations, recordedHTTPObservation{
		method:   method,
		route:    route,
		status:   status,
		duration: duration,
	})
}

func TestMetricsMiddlewareRecordsRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	metrics := &fakeHTTPMetrics{}
	engine := gin.New()
	engine.Use(Metrics(metrics))
	engine.GET("/api/v1/videos/:id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/videos/123", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", response.Code)
	}
	if metrics.inFlight != 0 {
		t.Fatalf("expected in-flight gauge to return to zero, got %d", metrics.inFlight)
	}
	if len(metrics.observations) != 1 {
		t.Fatalf("expected one observation, got %d", len(metrics.observations))
	}
	got := metrics.observations[0]
	if got.method != http.MethodGet {
		t.Errorf("expected method GET, got %s", got.method)
	}
	if got.route != "/api/v1/videos/:id" {
		t.Errorf("expected route template, got %s", got.route)
	}
	if got.status != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", got.status)
	}
	if got.duration < 0 {
		t.Errorf("expected non-negative duration, got %v", got.duration)
	}
}
