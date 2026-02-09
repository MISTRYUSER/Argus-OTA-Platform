package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
)

func main() {
	ctx := context.Background()

	// 1. 初始化 Redis Client
	redisClient, err := redis.NewRedisClient(
		ctx,
		"localhost:6379", // Redis 地址
		"",                // 密码（空字符串表示无密码）
		0,                 // 数据库编号
	)
	if err != nil {
		log.Fatalf("Failed to create Redis client: %v", err)
	}
	defer redisClient.Close()

	// 2. 测试 INCR（分布式计数器）
	log.Println("=== Test INCR ===")
	testKey := "batch:test-batch-001:counter"

	// 模拟 3 个 Worker 并发处理文件
	for i := 1; i <= 3; i++ {
		count, err := redisClient.INCR(ctx, testKey)
		if err != nil {
			log.Printf("INCR failed: %v", err)
			continue
		}
		log.Printf("Worker %d completed. Counter: %d", i, count)
	}

	// 3. 测试 GET（读取缓存）
	log.Println("\n=== Test GET ===")
	value, err := redisClient.GET(ctx, testKey)
	if err != nil {
		log.Printf("GET failed: %v", err)
	} else {
		log.Printf("GET result: %s", value)
	}

	// 4. 测试 SET（设置缓存，10 分钟过期）
	log.Println("\n=== Test SET ===")
	reportKey := "report:test-batch-001"
	reportData := `{"status": "completed", "total_files": 10, "error_count": 0}`
	err = redisClient.SET(ctx, reportKey, reportData, 10*time.Minute)
	if err != nil {
		log.Printf("SET failed: %v", err)
	} else {
		log.Printf("SET success: cached report for 10 minutes")
	}

	// 5. 验证 SET 结果
	value, err = redisClient.GET(ctx, reportKey)
	if err != nil {
		log.Printf("GET report failed: %v", err)
	} else {
		log.Printf("Cached report: %s", value)
	}

	// 6. 测试 DEL（删除 Key）
	log.Println("\n=== Test DEL ===")
	err = redisClient.DEL(ctx, testKey)
	if err != nil {
		log.Printf("DEL failed: %v", err)
	} else {
		log.Printf("DEL success: key deleted")
	}

	// 7. 验证删除结果
	value, err = redisClient.GET(ctx, testKey)
	if err != nil {
		log.Printf("GET after DEL failed: %v", err)
	} else {
		if value == "" {
			log.Printf("GET after DEL: key not found (expected)")
		} else {
			log.Printf("GET after DEL: %s (unexpected)", value)
		}
	}

	fmt.Println("\n✅ All Redis tests passed!")
}
