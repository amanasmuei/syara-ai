// Package storage provides vector store implementation with pgvector.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// VectorStore defines the interface for vector storage operations.
type VectorStore interface {
	// Insert operations
	Upsert(ctx context.Context, chunk DocumentChunk) error
	UpsertBatch(ctx context.Context, chunks []DocumentChunk) error

	// Search operations
	Search(ctx context.Context, query SearchQuery) ([]RetrievedChunk, error)
	SearchBySource(ctx context.Context, query SearchQuery, sourceType string) ([]RetrievedChunk, error)

	// Management
	Delete(ctx context.Context, chunkID string) error
	DeleteByDocument(ctx context.Context, documentID string) error
	GetByID(ctx context.Context, chunkID string) (*DocumentChunk, error)

	// Full-text search
	KeywordSearch(ctx context.Context, query string, opts KeywordSearchOptions) ([]RetrievedChunk, error)

	// Health check
	Health(ctx context.Context) error
}

// DocumentChunk represents a document chunk for vector storage.
type DocumentChunk struct {
	ID               uuid.UUID       `json:"id"`
	DocumentID       uuid.UUID       `json:"document_id"`
	Content          string          `json:"content"`
	Embedding        []float32       `json:"embedding,omitempty"`
	PageNumber       int             `json:"page_number"`
	SectionTitle     string          `json:"section_title"`
	SectionHierarchy []string        `json:"section_hierarchy"`
	ChunkIndex       int             `json:"chunk_index"`
	StartChar        int             `json:"start_char"`
	EndChar          int             `json:"end_char"`
	TokenCount       int             `json:"token_count"`
	Metadata         json.RawMessage `json:"metadata"`
	CreatedAt        time.Time       `json:"created_at"`
}

// SearchQuery represents a vector similarity search query.
type SearchQuery struct {
	Embedding []float32
	TopK      int
	MinScore  float64
	Filters   SearchFilters
}

// SearchFilters represents filters for search queries.
type SearchFilters struct {
	SourceType     string    // 'bnm', 'aaoifi', 'other'
	Categories     []string  // Document categories
	DateRange      DateRange // Effective date range
	StandardNumber string    // Specific standard number
	DocumentIDs    []string  // Specific document IDs
}

// DateRange represents a date range filter.
type DateRange struct {
	Start time.Time
	End   time.Time
}

// RetrievedChunk represents a chunk with similarity score.
type RetrievedChunk struct {
	DocumentChunk
	Similarity    float64 `json:"similarity"`
	SourceType    string  `json:"source_type"`
	DocumentTitle string  `json:"document_title"`
	Category      string  `json:"category"`
	StandardNum   string  `json:"standard_number"`
	EffectiveDate time.Time  `json:"effective_date,omitempty"`
	// Visual citation data
	PageImagePath   string          `json:"page_image_path,omitempty"`
	ThumbnailPath   string          `json:"thumbnail_path,omitempty"`
	BoundingBox     json.RawMessage `json:"bounding_box,omitempty"`
}

// KeywordSearchOptions represents options for keyword search.
type KeywordSearchOptions struct {
	TopK       int
	SourceType string
	Categories []string
}

// PgVectorStore implements VectorStore using PostgreSQL with pgvector.
type PgVectorStore struct {
	db     *PostgresDB
	logger *slog.Logger
}

// NewPgVectorStore creates a new PgVectorStore instance.
func NewPgVectorStore(db *PostgresDB, logger *slog.Logger) *PgVectorStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &PgVectorStore{
		db:     db,
		logger: logger.With("component", "vector_store"),
	}
}

// Health checks database connectivity.
func (vs *PgVectorStore) Health(ctx context.Context) error {
	return vs.db.PingContext(ctx)
}

// Upsert inserts or updates a single chunk.
func (vs *PgVectorStore) Upsert(ctx context.Context, chunk DocumentChunk) error {
	start := time.Now()
	defer func() {
		vs.logger.Debug("upsert completed",
			"chunk_id", chunk.ID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Generate ID if not provided
	if chunk.ID == uuid.Nil {
		chunk.ID = uuid.New()
	}

	// Convert embedding to pgvector format
	embeddingStr := embeddingToString(chunk.Embedding)

	query := `
		INSERT INTO chunks (
			id, document_id, content, embedding, page_number, section_title,
			section_hierarchy, chunk_index, start_char, end_char, token_count, metadata
		) VALUES (
			$1, $2, $3, $4::vector, $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (id) DO UPDATE SET
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			page_number = EXCLUDED.page_number,
			section_title = EXCLUDED.section_title,
			section_hierarchy = EXCLUDED.section_hierarchy,
			chunk_index = EXCLUDED.chunk_index,
			start_char = EXCLUDED.start_char,
			end_char = EXCLUDED.end_char,
			token_count = EXCLUDED.token_count,
			metadata = EXCLUDED.metadata
	`

	metadata := chunk.Metadata
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	_, err := vs.db.ExecContext(ctx, query,
		chunk.ID,
		chunk.DocumentID,
		chunk.Content,
		embeddingStr,
		nullInt(chunk.PageNumber),
		nullString(chunk.SectionTitle),
		chunk.SectionHierarchy,
		chunk.ChunkIndex,
		nullInt(chunk.StartChar),
		nullInt(chunk.EndChar),
		nullInt(chunk.TokenCount),
		metadata,
	)

	if err != nil {
		vs.logger.Error("failed to upsert chunk",
			"chunk_id", chunk.ID,
			"error", err,
		)
		return fmt.Errorf("failed to upsert chunk: %w", err)
	}

	return nil
}

// UpsertBatch inserts or updates multiple chunks efficiently.
func (vs *PgVectorStore) UpsertBatch(ctx context.Context, chunks []DocumentChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	start := time.Now()
	defer func() {
		vs.logger.Info("batch upsert completed",
			"count", len(chunks),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Use transaction for batch insert
	return vs.db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks (
				id, document_id, content, embedding, page_number, section_title,
				section_hierarchy, chunk_index, start_char, end_char, token_count, metadata
			) VALUES (
				$1, $2, $3, $4::vector, $5, $6, $7, $8, $9, $10, $11, $12
			)
			ON CONFLICT (id) DO UPDATE SET
				content = EXCLUDED.content,
				embedding = EXCLUDED.embedding,
				page_number = EXCLUDED.page_number,
				section_title = EXCLUDED.section_title,
				section_hierarchy = EXCLUDED.section_hierarchy,
				chunk_index = EXCLUDED.chunk_index,
				start_char = EXCLUDED.start_char,
				end_char = EXCLUDED.end_char,
				token_count = EXCLUDED.token_count,
				metadata = EXCLUDED.metadata
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare statement: %w", err)
		}
		defer stmt.Close()

		for i, chunk := range chunks {
			// Generate ID if not provided
			if chunk.ID == uuid.Nil {
				chunk.ID = uuid.New()
			}

			embeddingStr := embeddingToString(chunk.Embedding)
			metadata := chunk.Metadata
			if metadata == nil {
				metadata = json.RawMessage("{}")
			}

			_, err := stmt.ExecContext(ctx,
				chunk.ID,
				chunk.DocumentID,
				chunk.Content,
				embeddingStr,
				nullInt(chunk.PageNumber),
				nullString(chunk.SectionTitle),
				chunk.SectionHierarchy,
				chunk.ChunkIndex,
				nullInt(chunk.StartChar),
				nullInt(chunk.EndChar),
				nullInt(chunk.TokenCount),
				metadata,
			)
			if err != nil {
				vs.logger.Error("failed to upsert chunk in batch",
					"index", i,
					"chunk_id", chunk.ID,
					"error", err,
				)
				return fmt.Errorf("failed to upsert chunk %d: %w", i, err)
			}
		}

		return nil
	})
}

// Search performs vector similarity search.
func (vs *PgVectorStore) Search(ctx context.Context, query SearchQuery) ([]RetrievedChunk, error) {
	start := time.Now()
	defer func() {
		vs.logger.Debug("search completed",
			"top_k", query.TopK,
			"min_score", query.MinScore,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Set defaults
	if query.TopK <= 0 {
		query.TopK = 10
	}
	if query.MinScore <= 0 {
		query.MinScore = 0.5
	}

	embeddingStr := embeddingToString(query.Embedding)

	// Build dynamic query with filters
	var conditions []string
	var args []interface{}
	argIdx := 1

	// Base condition - only active documents with embeddings
	conditions = append(conditions, "d.is_active = true")
	conditions = append(conditions, "c.embedding IS NOT NULL")

	// Similarity threshold
	conditions = append(conditions, fmt.Sprintf("1 - (c.embedding <=> $%d::vector) > $%d", argIdx, argIdx+1))
	args = append(args, embeddingStr, query.MinScore)
	argIdx += 2

	// Apply filters
	if query.Filters.SourceType != "" {
		conditions = append(conditions, fmt.Sprintf("d.source_type = $%d", argIdx))
		args = append(args, query.Filters.SourceType)
		argIdx++
	}

	if len(query.Filters.Categories) > 0 {
		placeholders := make([]string, len(query.Filters.Categories))
		for i := range query.Filters.Categories {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, query.Filters.Categories[i])
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("d.category IN (%s)", strings.Join(placeholders, ",")))
	}

	if query.Filters.StandardNumber != "" {
		conditions = append(conditions, fmt.Sprintf("d.standard_number = $%d", argIdx))
		args = append(args, query.Filters.StandardNumber)
		argIdx++
	}

	if !query.Filters.DateRange.Start.IsZero() {
		conditions = append(conditions, fmt.Sprintf("d.effective_date >= $%d", argIdx))
		args = append(args, query.Filters.DateRange.Start)
		argIdx++
	}

	if !query.Filters.DateRange.End.IsZero() {
		conditions = append(conditions, fmt.Sprintf("d.effective_date <= $%d", argIdx))
		args = append(args, query.Filters.DateRange.End)
		argIdx++
	}

	if len(query.Filters.DocumentIDs) > 0 {
		placeholders := make([]string, len(query.Filters.DocumentIDs))
		for i := range query.Filters.DocumentIDs {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, query.Filters.DocumentIDs[i])
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("c.document_id IN (%s)", strings.Join(placeholders, ",")))
	}

	// Add TopK as last argument
	args = append(args, query.TopK)

	sqlQuery := fmt.Sprintf(`
		SELECT
			c.id,
			c.document_id,
			c.content,
			c.page_number,
			c.section_title,
			c.section_hierarchy,
			c.chunk_index,
			c.start_char,
			c.end_char,
			c.token_count,
			c.metadata,
			c.created_at,
			1 - (c.embedding <=> $1::vector) as similarity,
			d.source_type,
			d.title as document_title,
			d.category,
			d.standard_number,
			d.effective_date,
			cv.page_image_path,
			cv.thumbnail_path,
			cv.bounding_box
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		LEFT JOIN chunk_visuals cv ON c.id = cv.chunk_id
		WHERE %s
		ORDER BY c.embedding <=> $1::vector
		LIMIT $%d
	`, strings.Join(conditions, " AND "), argIdx)

	rows, err := vs.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		vs.logger.Error("search query failed", "error", err)
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	return vs.scanRetrievedChunks(rows)
}

// SearchBySource performs vector similarity search filtered by source type.
func (vs *PgVectorStore) SearchBySource(ctx context.Context, query SearchQuery, sourceType string) ([]RetrievedChunk, error) {
	query.Filters.SourceType = sourceType
	return vs.Search(ctx, query)
}

// KeywordSearch performs full-text search using PostgreSQL tsvector.
func (vs *PgVectorStore) KeywordSearch(ctx context.Context, queryText string, opts KeywordSearchOptions) ([]RetrievedChunk, error) {
	start := time.Now()
	defer func() {
		vs.logger.Debug("keyword search completed",
			"query", queryText,
			"top_k", opts.TopK,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Build conditions
	var conditions []string
	var args []interface{}
	argIdx := 1

	// Full-text search condition
	conditions = append(conditions, fmt.Sprintf("to_tsvector('english', c.content) @@ plainto_tsquery('english', $%d)", argIdx))
	args = append(args, queryText)
	argIdx++

	// Base conditions
	conditions = append(conditions, "d.is_active = true")

	// Source type filter
	if opts.SourceType != "" {
		conditions = append(conditions, fmt.Sprintf("d.source_type = $%d", argIdx))
		args = append(args, opts.SourceType)
		argIdx++
	}

	// Categories filter
	if len(opts.Categories) > 0 {
		placeholders := make([]string, len(opts.Categories))
		for i := range opts.Categories {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, opts.Categories[i])
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("d.category IN (%s)", strings.Join(placeholders, ",")))
	}

	args = append(args, opts.TopK)

	sqlQuery := fmt.Sprintf(`
		SELECT
			c.id,
			c.document_id,
			c.content,
			c.page_number,
			c.section_title,
			c.section_hierarchy,
			c.chunk_index,
			c.start_char,
			c.end_char,
			c.token_count,
			c.metadata,
			c.created_at,
			ts_rank(to_tsvector('english', c.content), plainto_tsquery('english', $1)) as similarity,
			d.source_type,
			d.title as document_title,
			d.category,
			d.standard_number,
			d.effective_date,
			cv.page_image_path,
			cv.thumbnail_path,
			cv.bounding_box
		FROM chunks c
		JOIN documents d ON c.document_id = d.id
		LEFT JOIN chunk_visuals cv ON c.id = cv.chunk_id
		WHERE %s
		ORDER BY ts_rank(to_tsvector('english', c.content), plainto_tsquery('english', $1)) DESC
		LIMIT $%d
	`, strings.Join(conditions, " AND "), argIdx)

	rows, err := vs.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		vs.logger.Error("keyword search failed", "error", err)
		return nil, fmt.Errorf("keyword search failed: %w", err)
	}
	defer rows.Close()

	return vs.scanRetrievedChunks(rows)
}

// Delete removes a chunk by ID.
func (vs *PgVectorStore) Delete(ctx context.Context, chunkID string) error {
	start := time.Now()
	defer func() {
		vs.logger.Debug("delete completed",
			"chunk_id", chunkID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	result, err := vs.db.ExecContext(ctx, "DELETE FROM chunks WHERE id = $1", chunkID)
	if err != nil {
		vs.logger.Error("failed to delete chunk", "chunk_id", chunkID, "error", err)
		return fmt.Errorf("failed to delete chunk: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("chunk not found: %s", chunkID)
	}

	return nil
}

// DeleteByDocument removes all chunks for a document.
func (vs *PgVectorStore) DeleteByDocument(ctx context.Context, documentID string) error {
	start := time.Now()
	defer func() {
		vs.logger.Debug("delete by document completed",
			"document_id", documentID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	result, err := vs.db.ExecContext(ctx, "DELETE FROM chunks WHERE document_id = $1", documentID)
	if err != nil {
		vs.logger.Error("failed to delete chunks by document", "document_id", documentID, "error", err)
		return fmt.Errorf("failed to delete chunks by document: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	vs.logger.Info("deleted chunks for document",
		"document_id", documentID,
		"count", rowsAffected,
	)

	return nil
}

// GetByID retrieves a chunk by ID.
func (vs *PgVectorStore) GetByID(ctx context.Context, chunkID string) (*DocumentChunk, error) {
	start := time.Now()
	defer func() {
		vs.logger.Debug("get by id completed",
			"chunk_id", chunkID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	query := `
		SELECT
			id, document_id, content, page_number, section_title,
			section_hierarchy, chunk_index, start_char, end_char,
			token_count, metadata, created_at
		FROM chunks
		WHERE id = $1
	`

	var chunk DocumentChunk
	var pageNumber, startChar, endChar, tokenCount sql.NullInt32
	var sectionTitle sql.NullString

	err := vs.db.QueryRowContext(ctx, query, chunkID).Scan(
		&chunk.ID,
		&chunk.DocumentID,
		&chunk.Content,
		&pageNumber,
		&sectionTitle,
		&chunk.SectionHierarchy,
		&chunk.ChunkIndex,
		&startChar,
		&endChar,
		&tokenCount,
		&chunk.Metadata,
		&chunk.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		vs.logger.Error("failed to get chunk", "chunk_id", chunkID, "error", err)
		return nil, fmt.Errorf("failed to get chunk: %w", err)
	}

	chunk.PageNumber = int(pageNumber.Int32)
	chunk.SectionTitle = sectionTitle.String
	chunk.StartChar = int(startChar.Int32)
	chunk.EndChar = int(endChar.Int32)
	chunk.TokenCount = int(tokenCount.Int32)

	return &chunk, nil
}

// scanRetrievedChunks scans rows into RetrievedChunk slice.
func (vs *PgVectorStore) scanRetrievedChunks(rows *sql.Rows) ([]RetrievedChunk, error) {
	var results []RetrievedChunk

	for rows.Next() {
		var chunk RetrievedChunk
		var pageNumber, startChar, endChar, tokenCount sql.NullInt32
		var sectionTitle, category, standardNum sql.NullString
		var effectiveDate sql.NullTime
		var pageImagePath, thumbnailPath sql.NullString
		var boundingBox []byte

		err := rows.Scan(
			&chunk.ID,
			&chunk.DocumentID,
			&chunk.Content,
			&pageNumber,
			&sectionTitle,
			&chunk.SectionHierarchy,
			&chunk.ChunkIndex,
			&startChar,
			&endChar,
			&tokenCount,
			&chunk.Metadata,
			&chunk.CreatedAt,
			&chunk.Similarity,
			&chunk.SourceType,
			&chunk.DocumentTitle,
			&category,
			&standardNum,
			&effectiveDate,
			&pageImagePath,
			&thumbnailPath,
			&boundingBox,
		)
		if err != nil {
			vs.logger.Error("failed to scan chunk", "error", err)
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		chunk.PageNumber = int(pageNumber.Int32)
		chunk.SectionTitle = sectionTitle.String
		chunk.StartChar = int(startChar.Int32)
		chunk.EndChar = int(endChar.Int32)
		chunk.TokenCount = int(tokenCount.Int32)
		chunk.Category = category.String
		chunk.StandardNum = standardNum.String
		if effectiveDate.Valid {
			chunk.EffectiveDate = effectiveDate.Time
		}
		chunk.PageImagePath = pageImagePath.String
		chunk.ThumbnailPath = thumbnailPath.String
		if boundingBox != nil {
			chunk.BoundingBox = boundingBox
		}

		results = append(results, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// Helper functions

// embeddingToString converts a float32 slice to pgvector string format.
func embeddingToString(embedding []float32) string {
	if len(embedding) == 0 {
		return ""
	}

	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%f", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// nullString returns sql.NullString for empty strings.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullInt returns sql.NullInt32 for zero values.
func nullInt(i int) sql.NullInt32 {
	if i == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(i), Valid: true}
}
