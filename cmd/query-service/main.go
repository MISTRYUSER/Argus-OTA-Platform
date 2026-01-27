package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	redisinfra "github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/interfaces/http/handlers"
)

func main() {
	ctx := context.Background()

	// 1. 初始化 PostgreSQL
	db := initDB()

	// 2. 初始化 Redis
	redisClient := initRedis(ctx)

	// 3. 初始化 Repository
	batchRepo := postgres.NewPostgresBatchRepository(db)
	reportRepo := &mockReportRepository{} // TODO: 替换为真实的 ReportRepository

	// 4. 初始化 QueryService
	queryService := application.NewQueryService(batchRepo, reportRepo, redisClient)

	// 5. 初始化 HTTP Server
	router := gin.Default()
	queryHandler := handlers.NewQueryHandler(queryService)

	router.GET("/api/v1/batches/:id/report", queryHandler.GetReport)
	router.GET("/api/v1/batches/:id/progress", queryHandler.GetProgress)

	server := &http.Server{
		Addr:    ":8081",
		Handler: router,
	}

	// 6. 启动 HTTP Server
	go func() {
		log.Println("🚀 Query Service started on :8081")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 7. 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("🛑 Shutting down Query Service...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server.Shutdown(ctx)
	db.Close()
	redisClient.Close()

	log.Println("✅ Query Service stopped gracefully")
}

// initDB 初始化 PostgreSQL 连接（复用 Ingestor 的代码）
func initDB() *sql.DB {
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "argus")
	dbPassword := getEnv("DB_PASSWORD", "argus_password")
	dbName := getEnv("DB_NAME", "argus_ota")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// 配置连接池
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ping 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Printf("[PostgreSQL] Connected to %s:%s/%s", dbHost, dbPort, dbName)
	return db
}

// initRedis 初始化 Redis 连接（复用 Orchestrator 的代码）
func initRedis(ctx context.Context) *redisinfra.RedisClient {
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")

	redisClient, err := redisinfra.NewRedisClient(ctx, redisAddr, redisPassword, 0)
	if err != nil {
		log.Fatalf("Failed to create Redis client: %v", err)
	}

	log.Printf("[Redis] Connected to %s", redisAddr)
	return redisClient
}

// getEnv 读取环境变量，提供默认值
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// mockReportRepository Mock ReportRepository（临时实现）
// TODO: 替换为真实的 PostgresReportRepository
type mockReportRepository struct{}

func (m *mockReportRepository) Save(ctx context.Context, report *domain.Report) error {
	log.Printf("[MockReportRepository] Save: %s (batchID=%s)", report.ID, report.BatchID)
	return nil
}

func (m *mockReportRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Report, error) {
	return nil, nil
}

func (m *mockReportRepository) FindByBatchID(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	return nil, nil
}

