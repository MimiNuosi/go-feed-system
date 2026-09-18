package health

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler 提供两个目的不同的健康检查。
//
// liveness: 当前进程是否还能响应，失败时才考虑重启。
// readiness: 当前实例是否可以接收业务流量，失败时应先摘除流量。
type Handler struct {
	version string
}

func NewHandler(version string) *Handler {
	return &Handler{
		version: version,
	}
}

type response struct {
	Status  string `json:"status"`
	Check   string `json:"check"`
	Version string `json:"version"`
}

func (h *Handler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, response{
		Status:  "ok",
		Check:   "live",
		Version: h.version,
	})
}

func (h *Handler) Ready(c *gin.Context) {
	// TODO: 在接入 MySQL、Redis、RabbitMQ 后，为每个必要依赖增加带超时的检查。
	//
	// 设计提醒：
	// 1. 不要让任一依赖无限等待，否则 readiness 自己会卡死。
	// 2. 只有“缺失后无法提供任何业务”的依赖，才应该让 readiness 失败。
	// 3. 非核心功能应该降级，而不是把整个实例标记为不可用。
	c.JSON(http.StatusOK, response{
		Status:  "ok",
		Check:   "ready",
		Version: h.version,
	})
}
