package config

import "os"

type Config struct {
	BatchID     string
	DatabaseURL string
	GLMAPIKey   string
	GLMModel    string
}

func Load() *Config {
	return &Config{
		BatchID:     getenv("BATCH_ID", "test-batch-001"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"),
		GLMAPIKey:   os.Getenv("GLM_API_KEY"),
		GLMModel:    getenv("GLM_MODEL", "glm-4-flash"),
	}
}

func getenv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
