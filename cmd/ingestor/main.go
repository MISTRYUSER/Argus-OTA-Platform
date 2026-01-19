package main

import (

	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"

	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/minio"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/interfaces/http/handlers"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)
type Config struct {
	Server 	 ServerConfig
	Database DatabaseConfig
	MinIO    MinIOConfig
	Kafka    KafkaConfig
}

type ServerConfig struct {
	Port 	int 
	Timeout time.Duration
}
type DatabaseConfig struct {
	Host     string
	Port 	 int
	User     string
	Password string
	DBName   string
}
type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

type KafkaConfig struct {
	Brokers []string
	Topic   string
}
func getEnv(key , defaultValue string) string {
	if value := os.Getenv(key);value != "" {
		return value
	}
	return defaultValue
}
func mustAtoi(s string, field string) int  {
	i, err := strconv.Atoi(s)
    if err != nil {
        log.Fatalf("invalid %s: %s", field, s)
    }
    return i
}
func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
func loadConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:  		mustAtoi(getEnv("SERVER_PORT","8080"),"SERVER_PORT"),
			Timeout:	30 * time.Second,
		},
		Database: DatabaseConfig{
			Host:  		getEnv("DB_HOST","localhost"),
			Port:  		mustAtoi(getEnv("DB_PORT","5432"), "DB_PORT"),
			User: 		getEnv("DB_USER","postgres"),
			Password: 	getEnv("DB_PASSWORD",""),
			DBName: 	getEnv("DB_NAME","argus_ota"),
		},
		MinIO: MinIOConfig{
			Endpoint:  getEnv("MINIO_ENDPOINT", "localhost:9000"),
			AccessKey: getEnv("MINIO_ACCESS_KEY", ""),
			SecretKey: getEnv("MINIO_SECRET_KEY", ""),
			Bucket:    getEnv("MINIO_BUCKET", "argus-files"),
			UseSSL:    parseBool(getEnv("MINIO_USE_SSL", "false")),
		},
		Kafka: KafkaConfig{
			Brokers: []string{getEnv("KAFKA_BROKERS", "localhost:9092")},
			Topic:   getEnv("KAFKA_TOPIC", "batch-events"),
		},
	}
}
func initDB(cfg *Config) *sql.DB {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.DBName,
	)
	db,err := sql.Open("postgres",dsn)
	if err != nil {
		log.Fatal("Failed to open database : ",err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)  // Idle 应该小于 Open
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping();err != nil {
		log.Fatal("Failed to ping database:", err)
	}
	log.Println("[DB] Database connected successfully")
    return db
}
func initMinIO(cfg *Config) *minio.MinIOClient {
	client, err := minio.NewMinIOClient(
		cfg.MinIO.Endpoint,
		cfg.MinIO.Bucket,
		cfg.MinIO.AccessKey,
		cfg.MinIO.SecretKey,
		cfg.MinIO.UseSSL,
	)
	if err != nil {
		log.Fatal("Failed to init MinIO:", err)
	}

	log.Println("[MinIO] Client initialized successfully")
	return client
}
func initKafkaProducer(cfg *Config) (messaging.KafkaEventPublisher, error) {
	producer, err := kafka.NewKafkaEventProducer(
		cfg.Kafka.Brokers,
		cfg.Kafka.Topic,
	)
	if err != nil {
		return nil, err
	}

	log.Println("[Kafka] Producer initialized successfully")
	return producer, nil
}
func initRouter(batchService *application.BatchService, minioClient *minio.MinIOClient) *gin.Engine {
	router := gin.Default()

	handler := handlers.NewBatchHandler(batchService, minioClient)
	handler.RegisterRoutes(router)

	return router
}
func startServer(router *gin.Engine,port string) *http.Server {
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 300 * time.Second, // 上传大文件需要长超时
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("[Server] Starting on port %s", port)
          if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
              log.Fatal("Server failed:", err)
          }
	}()
	return server
}
func gracefulShutdown(server *http.Server, db *sql.DB, kafkaProducer messaging.KafkaEventPublisher) {
	// 监听系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	log.Println("[Shutdown] Received shutdown signal")

	// 设置超时
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 关闭 HTTP Server
	if err := server.Shutdown(ctx); err != nil {
		log.Println("[Shutdown] Server shutdown error:", err)
	}

	// 关闭数据库
	if err := db.Close(); err != nil {
		log.Println("[Shutdown] DB close error:", err)
	}

	// 关闭 Kafka Producer
	if err := kafkaProducer.Close(); err != nil {
		log.Println("[Shutdown] Kafka close error:", err)
	}

	log.Println("[Shutdown] Graceful shutdown completed")
}
func main() {
	// 1. 加载配置
	cfg := loadConfig()

	// 2. 初始化基础设施
	db := initDB(cfg)
	minioClient := initMinIO(cfg)
	kafkaProducer, err := initKafkaProducer(cfg)
	if err != nil {
		log.Fatal("Failed to init Kafka:", err)
	}

	// 3. 初始化 Repository
	batchRepo := postgres.NewPostgresBatchRepository(db)

	// 4. 初始化 Service
	batchService := application.NewBatchService(batchRepo, kafkaProducer)

	// 5. 初始化 Router
	router := initRouter(batchService, minioClient)

	// 6. 启动 HTTP Server
	server := startServer(router, strconv.Itoa(cfg.Server.Port))

	// 7. 优雅关闭
	gracefulShutdown(server, db, kafkaProducer)
}
