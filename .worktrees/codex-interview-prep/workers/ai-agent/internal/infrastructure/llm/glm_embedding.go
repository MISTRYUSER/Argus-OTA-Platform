package llm

import (
	"context"
	"fmt"
	"os"

	openaiemb "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/embedding"
)

type EmbeddingConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

func NewEmbeddingModel(conf *EmbeddingConfig) (embedding.Embedder, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if conf.APIKey == "" {
		conf.APIKey = os.Getenv("GLM_API_KEY")
	}

	// 📌 关键校验：确保 API Key 存在
	if conf.APIKey == "" {
		return nil, fmt.Errorf("missing API key (please set GLM_API_KEY env var)")
	}

	if conf.Model == "" {
		conf.Model = "embedding-2" // GLM-4 embedding 模型
	}

	if conf.BaseURL == "" {
		conf.BaseURL = "https://open.bigmodel.cn/api/paas/v4/" // 📌 HTTPS + 结尾斜杠
	}

	// 📌 使用 Eino 的 EmbeddingModel 接口
	// 自动获得：Token 统计、链路追踪、统一重试和熔断
	config := &openaiemb.EmbeddingConfig{
		APIKey:  conf.APIKey,
		BaseURL: conf.BaseURL,
		Model:   conf.Model,
	}

	embedModel, err := openaiemb.NewEmbedder(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding model: %w", err)
	}

	return embedModel, nil
}
