// Package testing provides test utilities including testcontainers setup.
package testing

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/lib/pq"
)

// ContainerConfig holds configuration for test containers.
type ContainerConfig struct {
	PostgresImage   string
	PostgresDB      string
	PostgresUser    string
	PostgresPass    string
	RedisImage      string
	StartupTimeout  time.Duration
	CleanupOnFinish bool
}

// DefaultContainerConfig returns a default container configuration.
func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		PostgresImage:   "pgvector/pgvector:pg16",
		PostgresDB:      "testdb",
		PostgresUser:    "testuser",
		PostgresPass:    "testpass",
		RedisImage:      "redis:7-alpine",
		StartupTimeout:  60 * time.Second,
		CleanupOnFinish: true,
	}
}

// TestContainers holds running test containers.
type TestContainers struct {
	PostgresContainer *postgres.PostgresContainer
	RedisContainer    *redis.RedisContainer
	PostgresConnStr   string
	RedisConnStr      string
	config            ContainerConfig
	logger            *slog.Logger
}

// NewTestContainers creates and starts test containers.
func NewTestContainers(ctx context.Context, config ContainerConfig, logger *slog.Logger) (*TestContainers, error) {
	if logger == nil {
		logger = slog.Default()
	}

	tc := &TestContainers{
		config: config,
		logger: logger.With("component", "testcontainers"),
	}

	return tc, nil
}

// StartPostgres starts a PostgreSQL container with pgvector extension.
func (tc *TestContainers) StartPostgres(ctx context.Context) error {
	tc.logger.Info("starting PostgreSQL container", "image", tc.config.PostgresImage)

	container, err := postgres.Run(ctx,
		tc.config.PostgresImage,
		postgres.WithDatabase(tc.config.PostgresDB),
		postgres.WithUsername(tc.config.PostgresUser),
		postgres.WithPassword(tc.config.PostgresPass),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(tc.config.StartupTimeout),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to start postgres container: %w", err)
	}

	tc.PostgresContainer = container

	// Get connection string
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("failed to get postgres connection string: %w", err)
	}

	tc.PostgresConnStr = connStr
	tc.logger.Info("PostgreSQL container started", "connection", connStr)

	// Enable pgvector extension
	if err := tc.enablePgVector(ctx); err != nil {
		return fmt.Errorf("failed to enable pgvector: %w", err)
	}

	return nil
}

// StartRedis starts a Redis container.
func (tc *TestContainers) StartRedis(ctx context.Context) error {
	tc.logger.Info("starting Redis container", "image", tc.config.RedisImage)

	container, err := redis.Run(ctx,
		tc.config.RedisImage,
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(tc.config.StartupTimeout),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to start redis container: %w", err)
	}

	tc.RedisContainer = container

	// Get connection string
	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		return fmt.Errorf("failed to get redis connection string: %w", err)
	}

	tc.RedisConnStr = connStr
	tc.logger.Info("Redis container started", "connection", connStr)

	return nil
}

// StartAll starts both PostgreSQL and Redis containers.
func (tc *TestContainers) StartAll(ctx context.Context) error {
	if err := tc.StartPostgres(ctx); err != nil {
		return err
	}
	if err := tc.StartRedis(ctx); err != nil {
		return err
	}
	return nil
}

// Cleanup terminates all running containers.
func (tc *TestContainers) Cleanup(ctx context.Context) error {
	tc.logger.Info("cleaning up test containers")

	var errs []error

	if tc.PostgresContainer != nil {
		if err := tc.PostgresContainer.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to terminate postgres: %w", err))
		}
	}

	if tc.RedisContainer != nil {
		if err := tc.RedisContainer.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to terminate redis: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	tc.logger.Info("test containers cleaned up")
	return nil
}

// enablePgVector enables the pgvector extension in the database.
func (tc *TestContainers) enablePgVector(ctx context.Context) error {
	db, err := sql.Open("postgres", tc.PostgresConnStr)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}
	defer db.Close()

	// Enable pgvector extension
	_, err = db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("failed to create vector extension: %w", err)
	}

	tc.logger.Info("pgvector extension enabled")
	return nil
}

// GetPostgresDB returns a database connection to the test PostgreSQL.
func (tc *TestContainers) GetPostgresDB(ctx context.Context) (*sql.DB, error) {
	if tc.PostgresConnStr == "" {
		return nil, fmt.Errorf("postgres container not started")
	}

	db, err := sql.Open("postgres", tc.PostgresConnStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return db, nil
}

// RunMigrations runs the database migrations for testing.
func (tc *TestContainers) RunMigrations(ctx context.Context) error {
	db, err := tc.GetPostgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	// Create necessary tables for testing
	migrations := []string{
		// Documents table
		`CREATE TABLE IF NOT EXISTS documents (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			source_type VARCHAR(50) NOT NULL,
			title TEXT NOT NULL,
			file_name VARCHAR(255),
			file_path TEXT,
			original_url TEXT,
			category VARCHAR(100),
			standard_number VARCHAR(50),
			effective_date DATE,
			total_pages INTEGER,
			language VARCHAR(10) DEFAULT 'en',
			is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Chunks table
		`CREATE TABLE IF NOT EXISTS chunks (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			content TEXT NOT NULL,
			embedding vector(1536),
			page_number INTEGER,
			section_title VARCHAR(500),
			section_hierarchy TEXT[],
			chunk_index INTEGER NOT NULL,
			start_char INTEGER,
			end_char INTEGER,
			token_count INTEGER,
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Chunk visuals table
		`CREATE TABLE IF NOT EXISTS chunk_visuals (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			chunk_id UUID NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
			page_image_path TEXT,
			thumbnail_path TEXT,
			highlighted_image_path TEXT,
			bounding_box JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Conversations table
		`CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID,
			title VARCHAR(255) NOT NULL,
			summary TEXT,
			is_archived BOOLEAN DEFAULT false,
			message_count INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Messages table
		`CREATE TABLE IF NOT EXISTS messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			role VARCHAR(20) NOT NULL,
			content TEXT NOT NULL,
			citations JSONB DEFAULT '[]',
			tokens_used INTEGER,
			model_used VARCHAR(50),
			latency_ms INTEGER,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Create indexes for vector search
		`CREATE INDEX IF NOT EXISTS idx_chunks_embedding ON chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_source_type ON documents(source_type)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_category ON documents(category)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id)`,

		// Full-text search index
		`CREATE INDEX IF NOT EXISTS idx_chunks_content_fts ON chunks USING GIN (to_tsvector('english', content))`,
	}

	for i, migration := range migrations {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("migration %d failed: %w", i, err)
		}
	}

	tc.logger.Info("migrations completed successfully")
	return nil
}

// PostgresTestHelper provides helper methods for PostgreSQL testing.
type PostgresTestHelper struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewPostgresTestHelper creates a new PostgreSQL test helper.
func NewPostgresTestHelper(db *sql.DB, logger *slog.Logger) *PostgresTestHelper {
	if logger == nil {
		logger = slog.Default()
	}
	return &PostgresTestHelper{
		db:     db,
		logger: logger,
	}
}

// TruncateAll truncates all tables for a clean test state.
func (h *PostgresTestHelper) TruncateAll(ctx context.Context) error {
	tables := []string{
		"chunk_visuals",
		"chunks",
		"messages",
		"conversations",
		"documents",
	}

	for _, table := range tables {
		if _, err := h.db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			return fmt.Errorf("failed to truncate %s: %w", table, err)
		}
	}

	return nil
}

// InsertTestDocument inserts a test document and returns its ID.
func (h *PostgresTestHelper) InsertTestDocument(ctx context.Context, doc TestDocument) (string, error) {
	var id string
	err := h.db.QueryRowContext(ctx, `
		INSERT INTO documents (source_type, title, category, standard_number, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, doc.SourceType, doc.Title, doc.Category, doc.StandardNumber, true).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("failed to insert document: %w", err)
	}

	return id, nil
}

// InsertTestChunk inserts a test chunk with embedding.
func (h *PostgresTestHelper) InsertTestChunk(ctx context.Context, chunk TestChunk) (string, error) {
	var id string
	embeddingStr := embedToString(chunk.Embedding)

	err := h.db.QueryRowContext(ctx, `
		INSERT INTO chunks (document_id, content, embedding, page_number, section_title, chunk_index)
		VALUES ($1, $2, $3::vector, $4, $5, $6)
		RETURNING id
	`, chunk.DocumentID, chunk.Content, embeddingStr, chunk.PageNumber, chunk.SectionTitle, chunk.ChunkIndex).Scan(&id)

	if err != nil {
		return "", fmt.Errorf("failed to insert chunk: %w", err)
	}

	return id, nil
}

// TestDocument represents a test document.
type TestDocument struct {
	SourceType     string
	Title          string
	Category       string
	StandardNumber string
}

// TestChunk represents a test chunk.
type TestChunk struct {
	DocumentID   string
	Content      string
	Embedding    []float32
	PageNumber   int
	SectionTitle string
	ChunkIndex   int
}

// embedToString converts embedding to pgvector format.
func embedToString(embedding []float32) string {
	if len(embedding) == 0 {
		return ""
	}
	result := "["
	for i, v := range embedding {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("%f", v)
	}
	result += "]"
	return result
}

// RedisTestHelper provides helper methods for Redis testing.
type RedisTestHelper struct {
	connStr string
	logger  *slog.Logger
}

// NewRedisTestHelper creates a new Redis test helper.
func NewRedisTestHelper(connStr string, logger *slog.Logger) *RedisTestHelper {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisTestHelper{
		connStr: connStr,
		logger:  logger,
	}
}

// GetConnectionString returns the Redis connection string.
func (h *RedisTestHelper) GetConnectionString() string {
	return h.connStr
}
