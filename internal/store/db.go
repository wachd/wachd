// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the database connection pool
type DB struct {
	pool *pgxpool.Pool
}

// NewDB creates a new database connection pool.
//
// The databaseURL must NOT contain the password when running in Kubernetes —
// pass a URL with no password (e.g. postgres://user@host/db?sslmode=require)
// and set POSTGRES_PASSWORD as a separate environment variable injected from
// a K8s Secret. This avoids URL-encoding issues with special characters in
// passwords, which is common with cloud-managed databases.
//
// For local development, the password can be included in DATABASE_URL as usual.
func NewDB(databaseURL string) (*DB, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Override password from POSTGRES_PASSWORD env var if set.
	// This allows the DATABASE_URL to omit the password entirely, which is
	// required when the password contains URL-unsafe characters (e.g. []{}|;:)
	// — common in cloud-managed PostgreSQL (Azure, GCP, RDS) auto-generated passwords.
	if pw := os.Getenv("POSTGRES_PASSWORD"); pw != "" {
		config.ConnConfig.Password = pw
	}

	// Set connection pool settings
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying connection pool
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}
