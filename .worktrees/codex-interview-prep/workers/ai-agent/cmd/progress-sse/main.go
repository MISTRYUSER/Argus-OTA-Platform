package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	httphandler "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/interfaces/http"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	port := getenv("SSE_PORT", "8090")

	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}
	defer client.Close()

	router := gin.Default()
	sseHandler := httphandler.NewSSEHandler(client)
	router.GET("/api/v1/batches/:id/progress/stream", sseHandler.StreamProgress)

	log.Printf("SSE server listening on :%s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("failed to run SSE server: %v", err)
	}
}

func getenv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
