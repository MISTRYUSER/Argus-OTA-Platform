package http

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type SSEHandler struct {
	redisClient *redis.Client
}

func NewSSEHandler(redisClient *redis.Client) *SSEHandler {
	return &SSEHandler{redisClient: redisClient}
}

// StreamProgress 订阅并转发 Redis 进度事件。
func (h *SSEHandler) StreamProgress(c *gin.Context) {
	if h.redisClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "redis client not initialized"})
		return
	}

	batchID := c.Param("id")
	if batchID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch id is required"})
		return
	}

	channel := fmt.Sprintf("batch:%s:progress", batchID)
	sub := h.redisClient.Subscribe(c.Request.Context(), channel)
	defer sub.Close()

	if _, err := sub.Receive(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("subscribe failed: %v", err)})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}
	flusher.Flush()

	ch := sub.Channel()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = c.Writer.Write([]byte("data: " + msg.Payload + "\n\n"))
			flusher.Flush()
		}
	}
}
