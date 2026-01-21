// Package main is the entry point for the Islamic Banking Agent API server.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/alqutdigital/islamic-banking-agent/internal/api"
	"github.com/alqutdigital/islamic-banking-agent/internal/api/handlers"
	"github.com/alqutdigital/islamic-banking-agent/internal/api/middleware"
	"github.com/alqutdigital/islamic-banking-agent/internal/config"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
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
	// Initialize Rate Limit Store
	// ============================
	// Using in-memory store by default
	// For production multi-instance deployments, use Redis-based store
	rateLimitStore := middleware.NewMemoryRateLimitStore()

	// ============================
	// Setup API Router
	// ============================
	deps := api.Dependencies{
		Logger:         log.Logger,
		DB:             newDatabaseAdapter(db),
		ObjectStorage:  newStorageAdapter(objectStorage),
		ChatService:    nil, // Chat service will be implemented separately
		RateLimitStore: rateLimitStore,
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
