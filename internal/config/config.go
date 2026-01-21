// Package config provides configuration management for the application.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration for the application.
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Redis    RedisConfig
	NATS     NATSConfig
	Storage  StorageConfig
	LLM      LLMConfig
	Crawler  CrawlerConfig
	Log      LogConfig
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Port            int
	Environment     string
	ShutdownTimeout int
}

// DatabaseConfig holds database configuration.
type DatabaseConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
}

// RedisConfig holds Redis configuration.
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// NATSConfig holds NATS configuration.
type NATSConfig struct {
	URL       string
	ClusterID string
}

// StorageConfig holds object storage configuration.
type StorageConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	UseSSL          bool
	Region          string
}

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider          string
	AnthropicKey      string
	OpenAIKey         string
	Model             string
	EmbeddingModel    string
	MaxTokens         int
	OllamaBaseURL     string
	LMStudioBaseURL   string
	EnableToolCalling bool
	Temperature       float64
}

// CrawlerConfig holds crawler configuration.
type CrawlerConfig struct {
	BNMBaseURL       string
	RateLimit        int
	UserAgent        string
	MaxConcurrency   int
	ChunkSize        int
	ChunkOverlap     int
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level     string
	Format    string
	AddSource bool
}

// Load loads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port:            getEnvAsInt("PORT", 8080),
			Environment:     getEnv("ENVIRONMENT", "development"),
			ShutdownTimeout: getEnvAsInt("SHUTDOWN_TIMEOUT", 30),
		},
		Database: DatabaseConfig{
			Host:         getEnv("DB_HOST", "localhost"),
			Port:         getEnvAsInt("DB_PORT", 5432),
			User:         getEnv("DB_USER", "postgres"),
			Password:     getEnv("DB_PASSWORD", ""),
			Database:     getEnv("DB_NAME", "islamic_banking"),
			SSLMode:      getEnv("DB_SSL_MODE", "disable"),
			MaxOpenConns: getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns: getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnvAsInt("REDIS_PORT", 6379),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvAsInt("REDIS_DB", 0),
		},
		NATS: NATSConfig{
			URL:       getEnv("NATS_URL", "nats://localhost:4222"),
			ClusterID: getEnv("NATS_CLUSTER_ID", "islamic-banking"),
		},
		Storage: StorageConfig{
			Endpoint:        getEnv("STORAGE_ENDPOINT", "localhost:9000"),
			AccessKeyID:     getEnv("STORAGE_ACCESS_KEY", "minioadmin"),
			SecretAccessKey: getEnv("STORAGE_SECRET_KEY", "minioadmin"),
			BucketName:      getEnv("STORAGE_BUCKET", "sharia-comply"),
			UseSSL:          getEnvAsBool("STORAGE_USE_SSL", false),
			Region:          getEnv("STORAGE_REGION", "us-east-1"),
		},
		LLM: LLMConfig{
			Provider:          getEnv("LLM_PROVIDER", "anthropic"),
			AnthropicKey:      getEnv("ANTHROPIC_API_KEY", ""),
			OpenAIKey:         getEnv("OPENAI_API_KEY", ""),
			Model:             getEnv("LLM_MODEL", "claude-sonnet-4-20250514"),
			EmbeddingModel:    getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
			MaxTokens:         getEnvAsInt("LLM_MAX_TOKENS", 4096),
			OllamaBaseURL:     getEnv("OLLAMA_BASE_URL", "http://localhost:11434/v1"),
			LMStudioBaseURL:   getEnv("LMSTUDIO_BASE_URL", "http://localhost:1234/v1"),
			EnableToolCalling: getEnvAsBool("LLM_ENABLE_TOOL_CALLING", true),
			Temperature:       getEnvAsFloat("LLM_TEMPERATURE", 0.3),
		},
		Crawler: CrawlerConfig{
			BNMBaseURL:     getEnv("BNM_BASE_URL", "https://www.bnm.gov.my"),
			RateLimit:      getEnvAsInt("CRAWLER_RATE_LIMIT", 2),
			UserAgent:      getEnv("CRAWLER_USER_AGENT", "ShariaComply-Bot/1.0"),
			MaxConcurrency: getEnvAsInt("CRAWLER_MAX_CONCURRENCY", 5),
			ChunkSize:      getEnvAsInt("CHUNK_SIZE", 512),
			ChunkOverlap:   getEnvAsInt("CHUNK_OVERLAP", 50),
		},
		Log: LogConfig{
			Level:     getEnv("LOG_LEVEL", "info"),
			Format:    getEnv("LOG_FORMAT", "json"),
			AddSource: getEnvAsBool("LOG_ADD_SOURCE", false),
		},
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	// For development, we don't require API keys
	if c.Server.Environment == "production" {
		if c.LLM.AnthropicKey == "" && c.LLM.OpenAIKey == "" {
			return fmt.Errorf("either ANTHROPIC_API_KEY or OPENAI_API_KEY must be set in production")
		}
	}
	return nil
}

// DSN returns the database connection string.
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

// URL returns the Redis connection URL.
func (c *RedisConfig) URL() string {
	if c.Password != "" {
		return fmt.Sprintf("redis://:%s@%s:%d/%d", c.Password, c.Host, c.Port, c.DB)
	}
	return fmt.Sprintf("redis://%s:%d/%d", c.Host, c.Port, c.DB)
}

// Helper functions for environment variable parsing
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}
