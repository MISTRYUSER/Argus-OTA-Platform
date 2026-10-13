package config

import (
	"fmt"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Mode, BatchID, DatabaseURL, Topic, Group string
	Brokers                                  []string
	Concurrency                              int
	TaskTimeout                              time.Duration
	LLM                                      *llm.GLM4Config
	Embedding                                *llm.EmbeddingConfig
	SharedQuotaScope                         string
	SharedQuotaGrace                         time.Duration
	SharedQuotaEnabled                       bool
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func Load() (*Config, error) {
	provider := getenv("LLM_PROVIDER", "glm")
	if os.Getenv("LLM_PROVIDER") == "" && (os.Getenv("DEEPSEEK_API_KEY_FILE") != "" || os.Getenv("DEEPSEEK_API_KEY") != "") {
		provider = "deepseek"
	}
	key := os.Getenv("LLM_API_KEY")
	keyFile := os.Getenv("LLM_API_KEY_FILE")
	base, model := "https://open.bigmodel.cn/api/paas/v4/", "glm-4.7-flash"
	switch provider {
	case "deepseek":
		base, model = "https://api.deepseek.com", "deepseek-flash"
		if key == "" {
			key = os.Getenv("DEEPSEEK_API_KEY")
		}
		if keyFile == "" {
			keyFile = os.Getenv("DEEPSEEK_API_KEY_FILE")
		}
	case "glm":
		if key == "" {
			key = os.Getenv("GLM_API_KEY")
		}
	default:
		if os.Getenv("LLM_BASE_URL") == "" {
			return nil, fmt.Errorf("LLM_BASE_URL is required for custom provider")
		}
	}
	if keyFile != "" {
		b, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read private API key file")
		}
		key = strings.TrimSpace(string(b))
	}
	if key == "" {
		return nil, fmt.Errorf("LLM API key is missing")
	}
	n, err := strconv.Atoi(getenv("AI_CONCURRENCY", "20"))
	if err != nil || n < 1 || n > 128 {
		return nil, fmt.Errorf("AI_CONCURRENCY must be 1..128")
	}
	timeout, err := time.ParseDuration(getenv("AI_TASK_TIMEOUT", "2m"))
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("AI_TASK_TIMEOUT must be positive")
	}
	llmConcurrency, e := strconv.Atoi(getenv("LLM_CONCURRENCY", "4"))
	if e != nil || llmConcurrency < 1 || llmConcurrency > 128 {
		return nil, fmt.Errorf("LLM_CONCURRENCY must be 1..128")
	}
	queue, e := strconv.Atoi(getenv("LLM_QUEUE_CAPACITY", "32"))
	if e != nil || queue < 0 || queue > 4096 {
		return nil, fmt.Errorf("LLM_QUEUE_CAPACITY must be 0..4096")
	}
	requestTimeout, e := time.ParseDuration(getenv("LLM_REQUEST_TIMEOUT", "60s"))
	if e != nil || requestTimeout < time.Millisecond || requestTimeout > 10*time.Minute {
		return nil, fmt.Errorf("LLM_REQUEST_TIMEOUT must be 1ms..10m")
	}
	grace, e := time.ParseDuration(getenv("LLM_QUOTA_CRASH_GRACE", "30s"))
	if e != nil || grace < time.Millisecond || grace > 10*time.Minute {
		return nil, fmt.Errorf("LLM_QUOTA_CRASH_GRACE must be 1ms..10m")
	}
	shared, e := strconv.ParseBool(getenv("LLM_SHARED_QUOTA", "true"))
	if e != nil {
		return nil, fmt.Errorf("LLM_SHARED_QUOTA must be boolean")
	}
	scope := getenv("LLM_QUOTA_SCOPE", provider+":"+getenv("LLM_MODEL", getenv("GLM_MODEL", model)))
	cfg := &Config{SharedQuotaEnabled: shared, SharedQuotaScope: scope, SharedQuotaGrace: grace, Mode: getenv("MODE", "kafka"), BatchID: os.Getenv("BATCH_ID"), DatabaseURL: getenv("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"), Topic: getenv("KAFKA_TOPIC", "batch-events"), Group: getenv("KAFKA_GROUP", "ai-agent-group"), Brokers: strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ","), Concurrency: n, TaskTimeout: timeout, LLM: &llm.GLM4Config{Governor: &llm.GovernorOptions{Concurrency: llmConcurrency, QueueCapacity: queue, Attempts: 3, Backoff: 200 * time.Millisecond, RequestTimeout: requestTimeout}, APIKey: key, BaseURL: getenv("LLM_BASE_URL", base), Model: getenv("LLM_MODEL", getenv("GLM_MODEL", model))}, Embedding: &llm.EmbeddingConfig{APIKey: os.Getenv("EMBEDDING_API_KEY"), BaseURL: getenv("EMBEDDING_BASE_URL", "https://open.bigmodel.cn/api/paas/v4/"), Model: getenv("EMBEDDING_MODEL", "embedding-2")}}
	if cfg.Mode != "kafka" && cfg.Mode != "once" {
		return nil, fmt.Errorf("MODE must be kafka or once")
	}
	if cfg.Mode == "once" && cfg.BatchID == "" {
		return nil, fmt.Errorf("BATCH_ID required in once mode")
	}
	return cfg, nil
}
