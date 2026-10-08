package health

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultCheckTimeout = time.Second

type Dependency struct {
	Name     string
	Critical bool
	Check    func(ctx context.Context) error
}

// Handler 提供两个目的不同的健康检查。
//
// liveness: 当前进程是否还能响应，失败时才考虑重启。
// readiness: 当前实例是否可以接收业务流量，失败时应先摘除流量。
type Handler struct {
	version      string
	dependencies []Dependency
	logger       *slog.Logger
	checkTimeout time.Duration
}

func NewHandler(
	version string,
	logger *slog.Logger,
	dependencies ...Dependency,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		version:      version,
		dependencies: dependencies,
		logger:       logger,
		checkTimeout: defaultCheckTimeout,
	}
}

type dependencyResponse struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Critical bool   `json:"critical"`
}

type response struct {
	Status       string               `json:"status"`
	Check        string               `json:"check"`
	Version      string               `json:"version"`
	Dependencies []dependencyResponse `json:"dependencies,omitempty"`
}

func (h *Handler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, response{
		Status:  "ok",
		Check:   "live",
		Version: h.version,
	})
}

func (h *Handler) Ready(c *gin.Context) {
	type checkResult struct {
		index      int
		dependency Dependency
		err        error
	}

	results := make(chan checkResult, len(h.dependencies))
	for index, dependency := range h.dependencies {
		go func() {
			ctx, cancel := context.WithTimeout(
				c.Request.Context(),
				h.checkTimeout,
			)
			defer cancel()

			results <- checkResult{
				index:      index,
				dependency: dependency,
				err:        dependency.Check(ctx),
			}
		}()
	}

	status := http.StatusOK
	overallStatus := "ok"
	dependencies := make([]dependencyResponse, len(h.dependencies))

	for range h.dependencies {
		result := <-results
		dependencyStatus := "up"
		if result.err != nil {
			dependencyStatus = "down"
			h.logger.Error(
				"dependency readiness check failed",
				"dependency", result.dependency.Name,
				"critical", result.dependency.Critical,
				"error", result.err,
			)
			if result.dependency.Critical {
				status = http.StatusServiceUnavailable
				overallStatus = "unavailable"
			} else if overallStatus == "ok" {
				overallStatus = "degraded"
			}
		}

		dependencies[result.index] = dependencyResponse{
			Name:     result.dependency.Name,
			Status:   dependencyStatus,
			Critical: result.dependency.Critical,
		}
	}

	c.JSON(status, response{
		Status:       overallStatus,
		Check:        "ready",
		Version:      h.version,
		Dependencies: dependencies,
	})
}
