// Package main is the entry point for the Islamic Banking Agent API server.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alqutdigital/islamic-banking-agent/internal/agent"
	"github.com/alqutdigital/islamic-banking-agent/internal/api"
	"github.com/alqutdigital/islamic-banking-agent/internal/api/handlers"
	"github.com/alqutdigital/islamic-banking-agent/internal/api/middleware"
	"github.com/alqutdigital/islamic-banking-agent/internal/config"
	"github.com/alqutdigital/islamic-banking-agent/internal/embedder"
	"github.com/alqutdigital/islamic-banking-agent/internal/llm"
	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/realtime"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/internal/tools"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/alqutdigital/islamic-banking-agent/pkg/shutdown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger
	log := logger.New(logger.Config{
		Level:     cfg.Log.Level,
		Format:    cfg.Log.Format,
		AddSource: cfg.Log.AddSource,
	})
	log.SetDefault()

	log.Info("starting Islamic Banking Agent",
		"version", "0.1.0",
		"environment", cfg.Server.Environment,
		"port", cfg.Server.Port,
	)

	// Initialize shutdown handler
	shutdownHandler := shutdown.New(log.Logger, time.Duration(cfg.Server.ShutdownTimeout)*time.Second)

	// ============================
	// Initialize Database
	// ============================
	var db *storage.PostgresDB
	if cfg.Database.Host != "" {
		pgConfig := storage.PostgresConfig{
			Host:         cfg.Database.Host,
			Port:         cfg.Database.Port,
			User:         cfg.Database.User,
			Password:     cfg.Database.Password,
			Database:     cfg.Database.Database,
			SSLMode:      cfg.Database.SSLMode,
			MaxOpenConns: cfg.Database.MaxOpenConns,
			MaxIdleConns: cfg.Database.MaxIdleConns,
		}

		var dbErr error
		db, dbErr = storage.NewPostgres(pgConfig)
		if dbErr != nil {
			log.Warn("failed to connect to database, running in limited mode", "error", dbErr)
		} else {
			log.Info("connected to database",
				"host", cfg.Database.Host,
				"database", cfg.Database.Database,
			)
			shutdownHandler.RegisterNamed("database", func(ctx context.Context) error {
				return db.Close()
			})
		}
	}

	// ============================
	// Initialize Object Storage
	// ============================
	var objectStorage *storage.MinIOStorage
	if cfg.Storage.Endpoint != "" {
		minioConfig := storage.MinIOConfig{
			Endpoint:        cfg.Storage.Endpoint,
			AccessKeyID:     cfg.Storage.AccessKeyID,
			SecretAccessKey: cfg.Storage.SecretAccessKey,
			BucketName:      cfg.Storage.BucketName,
			UseSSL:          cfg.Storage.UseSSL,
			Region:          cfg.Storage.Region,
		}

		var storageErr error
		objectStorage, storageErr = storage.NewMinIOStorage(minioConfig)
		if storageErr != nil {
			log.Warn("failed to connect to object storage, running in limited mode", "error", storageErr)
		} else {
			// Initialize bucket
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := objectStorage.InitBucket(ctx); err != nil {
				log.Warn("failed to initialize storage bucket", "error", err)
			}
			cancel()

			log.Info("connected to object storage",
				"endpoint", cfg.Storage.Endpoint,
				"bucket", cfg.Storage.BucketName,
			)
		}
	}

	// ============================
	// Initialize NATS Client
	// ============================
	var natsClient *realtime.NATSClient
	var wsHub *realtime.WSHub
	if cfg.NATS.URL != "" {
		natsConfig := realtime.NATSConfig{
			URL:       cfg.NATS.URL,
			ClusterID: cfg.NATS.ClusterID,
		}

		var natsErr error
		natsClient, natsErr = realtime.NewNATSClient(natsConfig, log.Logger)
		if natsErr != nil {
			log.Warn("failed to connect to NATS, real-time features disabled", "error", natsErr)
		} else {
			log.Info("connected to NATS", "url", cfg.NATS.URL)

			// Setup JetStream streams
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := natsClient.SetupStreams(ctx); err != nil {
				log.Warn("failed to setup NATS streams", "error", err)
			} else {
				log.Info("NATS JetStream streams initialized")
			}
			cancel()

			shutdownHandler.RegisterNamed("nats", func(ctx context.Context) error {
				return natsClient.Close()
			})

			// Initialize WebSocket Hub
			wsConfig := realtime.DefaultWSConfig()
			wsHub = realtime.NewWSHub(natsClient, wsConfig, log.Logger)

			// Start the WebSocket hub
			if err := wsHub.Start(context.Background()); err != nil {
				log.Warn("failed to start WebSocket hub", "error", err)
				wsHub = nil
			} else {
				log.Info("WebSocket hub started")
				shutdownHandler.RegisterNamed("websocket-hub", func(ctx context.Context) error {
					return wsHub.Stop(ctx)
				})
			}
		}
	} else {
		log.Warn("NATS not configured, real-time features disabled")
	}

	// ============================
	// Initialize Rate Limit Store
	// ============================
	// Using in-memory store by default
	// For production multi-instance deployments, use Redis-based store
	rateLimitStore := middleware.NewMemoryRateLimitStore()

	// ============================
	// Initialize Chat Service (AI Agent)
	// ============================
	var chatService handlers.ChatService
	if canInitializeLLM(cfg) {
		chatService = initChatService(cfg, db, log)
		if chatService != nil {
			log.Info("chat service initialized",
				"model", cfg.LLM.Model,
				"provider", cfg.LLM.Provider,
			)
		}
	} else {
		log.Warn("chat service not initialized: no LLM provider configured")
	}

	// ============================
	// Setup API Router
	// ============================
	deps := api.Dependencies{
		Logger:         log.Logger,
		DB:             newDatabaseAdapter(db),
		ObjectStorage:  newStorageAdapter(objectStorage),
		ChatService:    chatService,
		RateLimitStore: rateLimitStore,
		WSHub:          wsHub,
	}

	routerConfig := api.DefaultRouterConfig()

	router := api.NewRouter(deps, routerConfig)

	// ============================
	// Initialize HTTP Server
	// ============================
	serverConfig := api.ServerConfig{
		Host:            "",
		Port:            cfg.Server.Port,
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: time.Duration(cfg.Server.ShutdownTimeout) * time.Second,
	}

	server := api.NewServer(router, serverConfig, log.Logger)

	// Register server shutdown
	shutdownHandler.RegisterNamed("http-server", func(ctx context.Context) error {
		return server.Shutdown(ctx)
	})

	// Start server in goroutine
	go func() {
		log.Info("HTTP server listening", "addr", server.Addr())
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	shutdownHandler.Wait()

	log.Info("server stopped")
	return nil
}

// databaseAdapter adapts PostgresDB to the handlers.Database interface.
type databaseAdapter struct {
	db *storage.PostgresDB
}

func newDatabaseAdapter(db *storage.PostgresDB) handlers.Database {
	if db == nil {
		return nil
	}
	return &databaseAdapter{db: db}
}

func (a *databaseAdapter) Health(ctx context.Context) error {
	return a.db.Health(ctx)
}

// Placeholder implementations - these need to be implemented based on actual database schema
func (a *databaseAdapter) CreateConversation(ctx context.Context, conv *handlers.Conversation) error {
	// TODO: Implement when conversation table is ready
	return fmt.Errorf("not implemented")
}

func (a *databaseAdapter) GetConversation(ctx context.Context, id uuid.UUID) (*handlers.Conversation, error) {
	// TODO: Implement when conversation table is ready
	return nil, fmt.Errorf("not found")
}

func (a *databaseAdapter) UpdateConversation(ctx context.Context, conv *handlers.Conversation) error {
	// TODO: Implement when conversation table is ready
	return fmt.Errorf("not implemented")
}

func (a *databaseAdapter) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	// TODO: Implement when conversation table is ready
	return fmt.Errorf("not implemented")
}

func (a *databaseAdapter) ListConversations(ctx context.Context, opts handlers.ListOptions) ([]*handlers.Conversation, int, error) {
	// TODO: Implement when conversation table is ready
	return []*handlers.Conversation{}, 0, nil
}

func (a *databaseAdapter) CreateMessage(ctx context.Context, msg *handlers.Message) error {
	// TODO: Implement when message table is ready
	return fmt.Errorf("not implemented")
}

func (a *databaseAdapter) GetMessages(ctx context.Context, conversationID uuid.UUID, opts handlers.ListOptions) ([]*handlers.Message, int, error) {
	// TODO: Implement when message table is ready
	return []*handlers.Message{}, 0, nil
}

func (a *databaseAdapter) GetDocument(ctx context.Context, id uuid.UUID) (*handlers.Document, error) {
	// TODO: Implement when document queries are ready
	return nil, fmt.Errorf("not found")
}

func (a *databaseAdapter) GetChunkVisual(ctx context.Context, chunkID uuid.UUID) (*handlers.ChunkVisual, error) {
	// TODO: Implement when chunk visual queries are ready
	return nil, fmt.Errorf("not found")
}

// storageAdapter adapts MinIOStorage to the handlers.ObjectStorage interface.
type storageAdapter struct {
	storage *storage.MinIOStorage
}

func newStorageAdapter(s *storage.MinIOStorage) handlers.ObjectStorage {
	if s == nil {
		return nil
	}
	return &storageAdapter{storage: s}
}

func (a *storageAdapter) Health(ctx context.Context) error {
	return a.storage.Health(ctx)
}

func (a *storageAdapter) GenerateSignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	return a.storage.GenerateSignedURL(ctx, path, expiry)
}

func (a *storageAdapter) Exists(ctx context.Context, path string) (bool, error) {
	return a.storage.Exists(ctx, path)
}

// ============================
// Chat Service Initialization
// ============================

// canInitializeLLM checks if we have enough configuration to initialize an LLM provider.
func canInitializeLLM(cfg *config.Config) bool {
	provider := strings.ToLower(cfg.LLM.Provider)
	switch provider {
	case "anthropic":
		return cfg.LLM.AnthropicKey != ""
	case "ollama", "lmstudio":
		return true // Local providers don't require API keys
	default:
		return cfg.LLM.AnthropicKey != "" // Default to anthropic behavior
	}
}

// createLLMProvider creates an LLM provider based on the configuration.
func createLLMProvider(cfg *config.Config, log *logger.Logger) (llm.Provider, error) {
	provider := strings.ToLower(cfg.LLM.Provider)

	// Determine base URL for local providers
	var baseURL string
	switch provider {
	case "ollama":
		baseURL = cfg.LLM.OllamaBaseURL
	case "lmstudio":
		baseURL = cfg.LLM.LMStudioBaseURL
	}

	providerCfg := llm.ProviderConfig{
		Provider:          provider,
		APIKey:            cfg.LLM.AnthropicKey,
		Model:             cfg.LLM.Model,
		BaseURL:           baseURL,
		MaxTokens:         cfg.LLM.MaxTokens,
		Temperature:       cfg.LLM.Temperature,
		EnableToolCalling: cfg.LLM.EnableToolCalling,
	}

	return llm.NewProvider(providerCfg, log.Logger)
}

// initChatService initializes the AI chat service with all dependencies.
func initChatService(cfg *config.Config, db *storage.PostgresDB, log *logger.Logger) handlers.ChatService {
	// Create LLM provider
	provider, err := createLLMProvider(cfg, log)
	if err != nil {
		log.Error("failed to create LLM provider", "error", err)
		return nil
	}
	log.Info("LLM provider created",
		"provider", provider.Name(),
		"model", provider.Model(),
		"supports_tools", provider.SupportsTools(),
	)

	// Initialize embedder for RAG (requires OpenAI key)
	var emb rag.Embedder
	if cfg.LLM.OpenAIKey != "" {
		embConfig := embedder.DefaultEmbedderConfig(cfg.LLM.OpenAIKey)
		embConfig.Model = cfg.LLM.EmbeddingModel
		emb, err = embedder.NewOpenAIEmbedder(embConfig, log)
		if err != nil {
			log.Warn("failed to initialize embedder", "error", err)
		} else {
			log.Info("embedder initialized", "model", embConfig.Model)
		}
	}

	// Initialize retriever (requires database and embedder)
	var retriever *rag.Retriever
	if db != nil && emb != nil {
		vectorStore := storage.NewPgVectorStore(db, log.Logger)
		retrieverConfig := rag.DefaultRetrieverConfig()
		retriever = rag.NewRetriever(vectorStore, emb, nil, log.Logger, retrieverConfig)
		log.Info("RAG retriever initialized")
	}

	// Initialize tools registry
	toolRegistry := tools.NewRegistry(log.Logger)

	// Register available tools
	if retriever != nil {
		// BNM search tool
		bnmTool := tools.NewSearchBNMTool(retriever)
		if err := toolRegistry.Register(bnmTool); err != nil {
			log.Warn("failed to register BNM tool", "error", err)
		}

		// AAOIFI search tool
		aaoifiTool := tools.NewSearchAAOIFITool(retriever)
		if err := toolRegistry.Register(aaoifiTool); err != nil {
			log.Warn("failed to register AAOIFI tool", "error", err)
		}

		// Compare standards tool
		compareTool := tools.NewCompareStandardsTool(retriever)
		if err := toolRegistry.Register(compareTool); err != nil {
			log.Warn("failed to register compare tool", "error", err)
		}

		// Latest circulars tool
		circularsTool := tools.NewGetLatestCircularsTool(retriever)
		if err := toolRegistry.Register(circularsTool); err != nil {
			log.Warn("failed to register circulars tool", "error", err)
		}

		// SC Malaysia search tool
		scTool := tools.NewSearchSCTool(retriever)
		if err := toolRegistry.Register(scTool); err != nil {
			log.Warn("failed to register SC tool", "error", err)
		}

		// IIFA (Majma Fiqh) search tool
		iifaTool := tools.NewSearchIIFATool(retriever)
		if err := toolRegistry.Register(iifaTool); err != nil {
			log.Warn("failed to register IIFA tool", "error", err)
		}

		// State Fatwa search tool
		fatwaTool := tools.NewSearchFatwaTool(retriever)
		if err := toolRegistry.Register(fatwaTool); err != nil {
			log.Warn("failed to register Fatwa tool", "error", err)
		}
	}

	// Initialize orchestrator config
	orchConfig := agent.DefaultOrchestratorConfig()
	orchConfig.Model = cfg.LLM.Model
	orchConfig.MaxTokens = cfg.LLM.MaxTokens
	orchConfig.Temperature = cfg.LLM.Temperature
	orchConfig.InitialRetrieval = retriever != nil

	// Create orchestrator with provider
	orchestrator, err := agent.NewOrchestrator(
		provider,
		retriever,
		toolRegistry,
		nil, // Memory can be added later
		log.Logger,
		orchConfig,
	)
	if err != nil {
		log.Error("failed to create orchestrator", "error", err)
		return nil
	}

	return newChatServiceAdapter(orchestrator)
}

// chatServiceAdapter adapts the agent.Orchestrator to the handlers.ChatService interface.
type chatServiceAdapter struct {
	orchestrator *agent.Orchestrator
}

func newChatServiceAdapter(orchestrator *agent.Orchestrator) handlers.ChatService {
	if orchestrator == nil {
		return nil
	}
	return &chatServiceAdapter{orchestrator: orchestrator}
}

// Process implements handlers.ChatService.
func (a *chatServiceAdapter) Process(ctx context.Context, req handlers.ChatRequest) (*handlers.ChatResult, error) {
	// Parse conversation ID if provided
	var convID uuid.UUID
	if req.ConversationID != "" {
		var err error
		convID, err = uuid.Parse(req.ConversationID)
		if err != nil {
			convID = uuid.New()
		}
	} else {
		convID = uuid.New()
	}

	// Call the orchestrator
	resp, err := a.orchestrator.Process(ctx, agent.AgentRequest{
		ConversationID: convID,
		UserMessage:    req.Message,
	})
	if err != nil {
		return nil, err
	}

	// Convert citations
	citations := make([]handlers.Citation, len(resp.Citations))
	for i, c := range resp.Citations {
		citations[i] = handlers.Citation{
			Index:      c.Index,
			ChunkID:    c.ChunkID,
			DocumentID: c.DocumentID,
			Source:     c.Source,
			Title:      c.Title,
			Page:       c.Page,
			Section:    c.Section,
			Content:    c.Content,
			Similarity: c.Similarity,
		}
	}

	return &handlers.ChatResult{
		ConversationID: resp.ConversationID.String(),
		Answer:         resp.Answer,
		Citations:      citations,
		Confidence:     calculateConfidence(resp),
		TokensUsed:     resp.TokensUsed.TotalTokens,
		ModelUsed:      resp.Model,
	}, nil
}

// calculateConfidence estimates a confidence score based on response characteristics.
func calculateConfidence(resp *agent.AgentResponse) float64 {
	if resp == nil {
		return 0
	}

	confidence := 0.5 // Base confidence

	// Higher confidence if we have citations
	if len(resp.Citations) > 0 {
		confidence += 0.2
	}
	if len(resp.Citations) >= 3 {
		confidence += 0.1
	}

	// Higher confidence if tools were used
	if len(resp.ToolCalls) > 0 {
		confidence += 0.1
	}

	// Cap at 0.95
	if confidence > 0.95 {
		confidence = 0.95
	}

	return confidence
}
