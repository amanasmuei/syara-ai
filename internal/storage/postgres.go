// Package storage provides database and object storage access.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresConfig holds PostgreSQL connection configuration.
type PostgresConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
	MaxLifetime  time.Duration
}

// PostgresDB wraps the database connection pool.
type PostgresDB struct {
	*sql.DB
	config PostgresConfig
}

// NewPostgres creates a new PostgreSQL connection pool.
func NewPostgres(cfg PostgresConfig) (*PostgresDB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	if cfg.MaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.MaxLifetime)
	} else {
		db.SetConnMaxLifetime(30 * time.Minute)
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresDB{DB: db, config: cfg}, nil
}

// Close closes the database connection pool.
func (db *PostgresDB) Close() error {
	return db.DB.Close()
}

// Health checks database connectivity.
func (db *PostgresDB) Health(ctx context.Context) error {
	return db.PingContext(ctx)
}

// WithTx executes a function within a transaction.
func (db *PostgresDB) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx error: %v, rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
