// Package main is the entry point for background workers.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/config"
	"github.com/alqutdigital/islamic-banking-agent/internal/realtime"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/alqutdigital/islamic-banking-agent/pkg/shutdown"
	"github.com/redis/go-redis/v9"
)

const (
	version = "0.1.0"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Create base context
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger
	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})
	log.SetDefault()

	log.Info("starting background worker",
		"version", version,
		"environment", cfg.Server.Environment,
	)

	// Initialize shutdown handler
	shutdownHandler := shutdown.New(log.Logger, time.Duration(cfg.Server.ShutdownTimeout)*time.Second)

	// Initialize Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// Verify Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	log.Info("connected to Redis", "host", cfg.Redis.Host, "port", cfg.Redis.Port)

	// Register Redis cleanup
	shutdownHandler.RegisterNamed("redis", func(ctx context.Context) error {
		return redisClient.Close()
	})

	// Initialize NATS client (ALQ-21)
	natsConfig := realtime.NATSConfig{
		URL:            cfg.NATS.URL,
		ClusterID:      cfg.NATS.ClusterID,
		MaxReconnects:  -1,
		ReconnectWait:  2 * time.Second,
		ConnectTimeout: 10 * time.Second,
	}

	natsClient, err := realtime.NewNATSClient(natsConfig, log.Logger)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	log.Info("connected to NATS", "url", cfg.NATS.URL)

	// Register NATS cleanup
	shutdownHandler.RegisterNamed("nats", func(ctx context.Context) error {
		return natsClient.Drain()
	})

	// Setup JetStream streams
	if err := natsClient.SetupStreams(ctx); err != nil {
		log.Warn("failed to setup JetStream streams (may already exist)", "error", err)
	}

	// Initialize Change Detector (ALQ-22)
	detectorConfig := realtime.DefaultChangeDetectorConfig()
	detectorConfig.UserAgent = cfg.Crawler.UserAgent

	changeDetector := realtime.NewChangeDetector(
		natsClient,
		redisClient,
		detectorConfig,
		log.Logger,
	)

	if err := changeDetector.Start(ctx); err != nil {
		return fmt.Errorf("failed to start change detector: %w", err)
	}
	log.Info("change detector started", "sources", len(detectorConfig.Sources))

	// Register change detector cleanup
	shutdownHandler.RegisterNamed("change_detector", func(ctx context.Context) error {
		return changeDetector.Stop(ctx)
	})

	// Initialize Worker Pool (ALQ-23)
	workerConfig := realtime.DefaultWorkerPoolConfig()

	workerPool := realtime.NewWorkerPool(
		natsClient,
		workerConfig,
		log.Logger,
		nil, // DocumentProcessor - to be implemented by crawler package
		nil, // Embedder - to be implemented by embedding package
		nil, // VectorStore - to be implemented by storage package
	)

	if err := workerPool.Start(ctx); err != nil {
		return fmt.Errorf("failed to start worker pool: %w", err)
	}
	log.Info("worker pool started",
		"crawl_workers", workerConfig.CrawlWorkers,
		"process_workers", workerConfig.ProcessWorkers,
		"index_workers", workerConfig.IndexWorkers,
	)

	// Register worker pool cleanup
	shutdownHandler.RegisterNamed("worker_pool", func(ctx context.Context) error {
		return workerPool.Stop(ctx)
	})

	// Initialize Cache Invalidator (ALQ-25)
	cacheConfig := realtime.DefaultCacheInvalidatorConfig()

	cacheInvalidator := realtime.NewCacheInvalidator(
		redisClient,
		natsClient,
		cacheConfig,
		log.Logger,
	)

	if err := cacheInvalidator.Start(ctx); err != nil {
		return fmt.Errorf("failed to start cache invalidator: %w", err)
	}
	log.Info("cache invalidator started")

	// Register cache invalidator cleanup
	shutdownHandler.RegisterNamed("cache_invalidator", func(ctx context.Context) error {
		return cacheInvalidator.Stop(ctx)
	})

	// Initialize WebSocket Hub (ALQ-24)
	wsConfig := realtime.DefaultWSConfig()
	wsHub := realtime.NewWSHub(natsClient, wsConfig, log.Logger)

	if err := wsHub.Start(ctx); err != nil {
		return fmt.Errorf("failed to start WebSocket hub: %w", err)
	}
	log.Info("WebSocket hub started")

	// Register WebSocket hub cleanup
	shutdownHandler.RegisterNamed("websocket_hub", func(ctx context.Context) error {
		return wsHub.Stop(ctx)
	})

	// Start HTTP server for WebSocket connections and health checks
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", wsHub.HandleWebSocket)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Check component health
		if !natsClient.IsConnected() {
			http.Error(w, "NATS not connected", http.StatusServiceUnavailable)
			return
		}
		if err := cacheInvalidator.Health(ctx); err != nil {
			http.Error(w, "Redis not healthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"worker_pool": %v,
			"websocket": %v,
			"cache_invalidator": %v,
			"change_detector": %v
		}`,
			toJSON(workerPool.GetMetrics()),
			toJSON(wsHub.GetMetrics()),
			toJSON(cacheInvalidator.GetMetrics()),
			toJSON(changeDetector.GetMetrics()),
		)
	})

	// Start HTTP server in background
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("starting HTTP server", "port", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	// Register HTTP server cleanup
	shutdownHandler.RegisterNamed("http_server", func(ctx context.Context) error {
		return server.Shutdown(ctx)
	})

	log.Info("worker started successfully",
		"version", version,
		"http_port", cfg.Server.Port,
		"nats_url", cfg.NATS.URL,
		"redis_host", cfg.Redis.Host,
	)

	// Wait for shutdown signal
	shutdownHandler.Wait()

	log.Info("worker stopped")
	return nil
}

// toJSON converts a map to JSON string for metrics output.
func toJSON(m map[string]interface{}) string {
	if m == nil {
		return "{}"
	}
	result := "{"
	first := true
	for k, v := range m {
		if !first {
			result += ","
		}
		first = false
		result += fmt.Sprintf(`"%s":`, k)
		switch val := v.(type) {
		case string:
			result += fmt.Sprintf(`"%s"`, val)
		case time.Time:
			if val.IsZero() {
				result += "null"
			} else {
				result += fmt.Sprintf(`"%s"`, val.Format(time.RFC3339))
			}
		default:
			result += fmt.Sprintf(`%v`, val)
		}
	}
	result += "}"
	return result
}
