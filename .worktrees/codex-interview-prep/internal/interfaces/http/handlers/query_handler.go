package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/internal/application"
)

type QueryHandler struct {
	queryService  *application.QueryService
}

func NewQueryHandler(queryService *application.QueryService) *QueryHandler {
	return &QueryHandler{
		queryService: queryService,
	}
}

 // GET /api/v1/batches/:id/report
 func (h *QueryHandler) GetReport(c *gin.Context) {
	batchIDStr := c.Param("id")
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid batch id"})
		return
	}

	report, err := h.queryService.GetReport(c.Request.Context(), batchID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, report)
}

// GetProgress 获取处理进度
// GET /api/v1/batches/:id/progress
func (h *QueryHandler) GetProgress(c *gin.Context) {
	batchIDStr := c.Param("id")
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid batch id"})
		return
	}

	progress, err := h.queryService.GetProgress(c.Request.Context(), batchID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, progress)
}

