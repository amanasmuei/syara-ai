package llm

import (
	"fmt"
	"log/slog"
	"strings"
)

// ProviderType represents the type of LLM provider.
type ProviderType string

const (
	ProviderAnthropic ProviderType = "anthropic"
	ProviderOpenAI    ProviderType = "openai"
	ProviderOllama    ProviderType = "ollama"
	ProviderLMStudio  ProviderType = "lmstudio"
)

// NewProvider creates a new LLM provider based on the configuration.
func NewProvider(cfg ProviderConfig, logger *slog.Logger) (Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}

	providerType := ProviderType(strings.ToLower(cfg.Provider))

	logger.Info("creating LLM provider",
		"provider", providerType,
		"model", cfg.Model,
	)

	switch providerType {
	case ProviderAnthropic:
		return NewAnthropicProvider(cfg, logger)

	case ProviderOpenAI:
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.openai.com/v1"
		}
		return NewOpenAICompatProvider(cfg, logger)

	case ProviderOllama:
		if cfg.BaseURL == "" {
			cfg.BaseURL = "http://localhost:11434/v1"
		}
		return NewOpenAICompatProvider(cfg, logger)

	case ProviderLMStudio:
		if cfg.BaseURL == "" {
			cfg.BaseURL = "http://localhost:1234/v1"
		}
		return NewOpenAICompatProvider(cfg, logger)

	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.Provider)
	}
}

// NewProviderFromConfig creates a provider using a simplified configuration struct.
// This is a convenience function for common use cases.
type SimplifiedConfig struct {
	Provider          string
	APIKey            string
	Model             string
	BaseURL           string
	MaxTokens         int
	Temperature       float64
	EnableToolCalling bool
}

// NewProviderFromSimplifiedConfig creates a provider from a simplified config.
func NewProviderFromSimplifiedConfig(cfg SimplifiedConfig, logger *slog.Logger) (Provider, error) {
	providerCfg := ProviderConfig{
		Provider:          cfg.Provider,
		APIKey:            cfg.APIKey,
		Model:             cfg.Model,
		BaseURL:           cfg.BaseURL,
		MaxTokens:         cfg.MaxTokens,
		Temperature:       cfg.Temperature,
		EnableToolCalling: cfg.EnableToolCalling,
	}

	// Set defaults
	if providerCfg.MaxTokens == 0 {
		providerCfg.MaxTokens = 4096
	}
	if providerCfg.Temperature == 0 {
		providerCfg.Temperature = 0.3
	}
	if !providerCfg.EnableToolCalling {
		providerCfg.EnableToolCalling = true
	}

	return NewProvider(providerCfg, logger)
}

// ValidateProviderConfig validates the provider configuration.
func ValidateProviderConfig(cfg ProviderConfig) error {
	providerType := ProviderType(strings.ToLower(cfg.Provider))

	switch providerType {
	case ProviderAnthropic:
		if cfg.APIKey == "" {
			return fmt.Errorf("API key is required for Anthropic provider")
		}

	case ProviderOpenAI:
		if cfg.APIKey == "" {
			return fmt.Errorf("API key is required for OpenAI provider")
		}

	case ProviderOllama, ProviderLMStudio:
		// Base URL is optional (defaults are used)
		// No API key required

	default:
		return fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}

	return nil
}

// GetDefaultModel returns the default model for a given provider.
func GetDefaultModel(provider string) string {
	switch ProviderType(strings.ToLower(provider)) {
	case ProviderAnthropic:
		return "claude-sonnet-4-20250514"
	case ProviderOpenAI:
		return "gpt-4o-mini"
	case ProviderOllama:
		return "llama3.2"
	case ProviderLMStudio:
		return "local-model"
	default:
		return ""
	}
}

// GetDefaultBaseURL returns the default base URL for a given provider.
func GetDefaultBaseURL(provider string) string {
	switch ProviderType(strings.ToLower(provider)) {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderOllama:
		return "http://localhost:11434/v1"
	case ProviderLMStudio:
		return "http://localhost:1234/v1"
	default:
		return ""
	}
}
