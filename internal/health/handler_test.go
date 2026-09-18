package health_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/health"
	"go-feed-system/internal/router"
)

func TestHandlerLive(t *testing.T) {
	t.Parallel()

	engine := newTestEngine()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
	if resp["check"] != "live" {
		t.Errorf("expected check 'live', got '%s'", resp["check"])
	}
	if resp["version"] != "test-version" {
		t.Errorf("expected version 'test-version', got '%s'", resp["version"])
	}
}

func TestHandlerReady(t *testing.T) {
	t.Parallel()

	engine := newTestEngine()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
	if resp["check"] != "ready" {
		t.Errorf("expected check 'ready', got '%s'", resp["check"])
	}
	if resp["version"] != "test-version" {
		t.Errorf("expected version 'test-version', got '%s'", resp["version"])
	}
}

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return router.New(logger, health.NewHandler("test-version"))
}
