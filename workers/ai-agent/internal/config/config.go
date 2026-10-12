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
	cfg := &Config{Mode: getenv("MODE", "kafka"), BatchID: os.Getenv("BATCH_ID"), DatabaseURL: getenv("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"), Topic: getenv("KAFKA_TOPIC", "batch-events"), Group: getenv("KAFKA_GROUP", "ai-agent-group"), Brokers: strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ","), Concurrency: n, TaskTimeout: timeout, LLM: &llm.GLM4Config{APIKey: key, BaseURL: getenv("LLM_BASE_URL", base), Model: getenv("LLM_MODEL", getenv("GLM_MODEL", model))}, Embedding: &llm.EmbeddingConfig{APIKey: os.Getenv("EMBEDDING_API_KEY"), BaseURL: getenv("EMBEDDING_BASE_URL", "https://open.bigmodel.cn/api/paas/v4/"), Model: getenv("EMBEDDING_MODEL", "embedding-2")}}
	if cfg.Mode != "kafka" && cfg.Mode != "once" {
		return nil, fmt.Errorf("MODE must be kafka or once")
	}
	if cfg.Mode == "once" && cfg.BatchID == "" {
		return nil, fmt.Errorf("BATCH_ID required in once mode")
	}
	return cfg, nil
}
