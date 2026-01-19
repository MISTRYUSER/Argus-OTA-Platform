package handlers

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/minio"
)
type batchHandler struct {
	batchService *application.BatchService
	minioClient  *minio.MinIOClient
}

func NewBatchHandler (
	batchService   *application.BatchService,
	minioClient    *minio.MinIOClient,
) *batchHandler {
	return &batchHandler{
		batchService: batchService,
		minioClient:  minioClient,
	}
}
func (h *batchHandler) CreateBatch(c *gin.Context) {
    var req struct {
        VehicleID       string `json:"vehicle_id" binding:"required"`
        VIN             string `json:"vin" binding:"required"`
        ExpectedWorkers int    `json:"expected_workers" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{
            "error": err.Error(),
        })
        return
    }

    batch, err := h.batchService.CreateBatch(c.Request.Context(), req.VehicleID, req.VIN,
    	req.ExpectedWorkers)
	if err != nil {
		c.JSON(500,gin.H{"error":err.Error()})
		return
	}

	c.JSON(201,gin.H{
		"batch_id": batch.ID,
		"status"  : batch.Status,
	})
}

func (h *batchHandler) UploadFile(c *gin.Context) {
	batchIDStr := c.Param("id")
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid batch id"})
		return
	}

	//stream read
	fileHeader,err := c.FormFile("file")
	if err != nil {
		c.JSON(400,gin.H{"error":"file is required"})
		return
	}

	file,err := fileHeader.Open() 
	if err != nil {
		c.JSON(500,gin.H{"error":err.Error()})
		return
	}
	defer file.Close()

	fileID := uuid.New()
	objectKey := fmt.Sprintf("%s/%s", batchIDStr, fileID)

	err = h.minioClient.PutObject(
		c.Request.Context(),
		objectKey,
		file,
		fileHeader.Size,
		"application/octet-stream",
	)
	if err != nil {
		c.JSON(500,gin.H{"error":err.Error()})
		return
	}

	err = h.batchService.AddFile(c.Request.Context(), batchID, fileID)
	if err != nil {
		c.JSON(500,gin.H{"error":err.Error()})
		return
	}
	c.JSON(201,gin.H{
		"file_id" : fileID,
		"size"	  : fileHeader.Size,
	})
}

func (h *batchHandler) CompleteUpload(c *gin.Context) {
	batchIDStr := c.Param("id")
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid batch id"})
		return
	}

	err = h.batchService.TransitionBatchStatus(
		c.Request.Context(),
		batchID,
		domain.BatchStatusUploaded,
	)
	if err != nil {
		c.JSON(500,gin.H{"error":err.Error()})
		return
	}
	c.JSON(200,gin.H{"message": "Batch completed,processing started"})
}

func (h *batchHandler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	{
		v1.POST("/batches", h.CreateBatch)
		v1.POST("/batches/:id/files", h.UploadFile)
		v1.POST("/batches/:id/complete", h.CompleteUpload)
	}
}

