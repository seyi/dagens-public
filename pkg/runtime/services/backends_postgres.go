// Copyright 2025 Apache Spark AI Agents
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

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSessionBackend provides PostgreSQL-backed session storage
type PostgresSessionBackend struct {
	pool *pgxpool.Pool
}

// PostgresConfig configures PostgreSQL connection
type PostgresConfig struct {
	Host            string
	Port            int
	Database        string
	User            string
	Password        string
	MaxConns        int           // Max connections (default: 20)
	MinConns        int           // Min connections (default: 5)
	ConnMaxLifetime time.Duration // Max connection lifetime (default: 1h)
	ConnMaxIdleTime time.Duration // Max idle time (default: 30m)
}

// NewPostgresSessionBackend creates a new PostgreSQL session backend
func NewPostgresSessionBackend(config PostgresConfig) (*PostgresSessionBackend, error) {
	if config.MaxConns == 0 {
		config.MaxConns = 20
	}
	if config.MinConns == 0 {
		config.MinConns = 5
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 1 * time.Hour
	}
	if config.ConnMaxIdleTime == 0 {
		config.ConnMaxIdleTime = 30 * time.Minute
	}
	if config.Port == 0 {
		config.Port = 5432
	}

	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=prefer",
		config.User, config.Password, config.Host, config.Port, config.Database,
	)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	poolConfig.MaxConns = int32(config.MaxConns)
	poolConfig.MinConns = int32(config.MinConns)
	poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = config.ConnMaxIdleTime

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresSessionBackend{
		pool: pool,
	}, nil
}

func (b *PostgresSessionBackend) Get(ctx context.Context, sessionID string) (*runtime.Session, error) {
	query := `
		SELECT id, user_id, state, event_history, created_at, updated_at
		FROM sessions
		WHERE id = $1
	`

	var session runtime.Session
	var stateJSON, eventsJSON []byte

	err := b.pool.QueryRow(ctx, query, sessionID).Scan(
		&session.ID,
		&session.UserID,
		&stateJSON,
		&eventsJSON,
		&session.CreatedAt,
		&session.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Unmarshal JSON fields
	if stateJSON != nil {
		if err := json.Unmarshal(stateJSON, &session.State); err != nil {
			return nil, fmt.Errorf("failed to unmarshal state: %w", err)
		}
	}
	if eventsJSON != nil {
		if err := json.Unmarshal(eventsJSON, &session.EventHistory); err != nil {
			return nil, fmt.Errorf("failed to unmarshal events: %w", err)
		}
	}

	return &session, nil
}

func (b *PostgresSessionBackend) Put(ctx context.Context, session *runtime.Session) error {
	// Update timestamp
	session.UpdatedAt = time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}

	// Marshal JSON fields
	stateJSON, err := json.Marshal(session.State)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	eventsJSON, err := json.Marshal(session.EventHistory)
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	query := `
		INSERT INTO sessions (id, user_id, state, event_history, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET user_id = EXCLUDED.user_id,
		    state = EXCLUDED.state,
		    event_history = EXCLUDED.event_history,
		    updated_at = EXCLUDED.updated_at
	`

	_, err = b.pool.Exec(ctx, query,
		session.ID,
		session.UserID,
		stateJSON,
		eventsJSON,
		session.CreatedAt,
		session.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to put session: %w", err)
	}

	return nil
}

func (b *PostgresSessionBackend) Delete(ctx context.Context, sessionID string) error {
	query := `DELETE FROM sessions WHERE id = $1`

	_, err := b.pool.Exec(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

func (b *PostgresSessionBackend) List(ctx context.Context, userID string) ([]*runtime.Session, error) {
	query := `
		SELECT id, user_id, state, event_history, created_at, updated_at
		FROM sessions
		WHERE user_id = $1
		ORDER BY updated_at DESC
	`

	rows, err := b.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*runtime.Session

	for rows.Next() {
		var session runtime.Session
		var stateJSON, eventsJSON []byte

		err := rows.Scan(
			&session.ID,
			&session.UserID,
			&stateJSON,
			&eventsJSON,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			continue // Skip invalid rows
		}

		// Unmarshal JSON fields
		if stateJSON != nil {
			json.Unmarshal(stateJSON, &session.State)
		}
		if eventsJSON != nil {
			json.Unmarshal(eventsJSON, &session.EventHistory)
		}

		sessions = append(sessions, &session)
	}

	return sessions, nil
}

func (b *PostgresSessionBackend) Close() error {
	b.pool.Close()
	return nil
}

// PostgresArtifactBackend provides PostgreSQL-backed artifact storage
type PostgresArtifactBackend struct {
	pool *pgxpool.Pool
}

// NewPostgresArtifactBackend creates a new PostgreSQL artifact backend
func NewPostgresArtifactBackend(config PostgresConfig) (*PostgresArtifactBackend, error) {
	if config.MaxConns == 0 {
		config.MaxConns = 20
	}
	if config.MinConns == 0 {
		config.MinConns = 5
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 1 * time.Hour
	}
	if config.ConnMaxIdleTime == 0 {
		config.ConnMaxIdleTime = 30 * time.Minute
	}
	if config.Port == 0 {
		config.Port = 5432
	}

	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=prefer",
		config.User, config.Password, config.Host, config.Port, config.Database,
	)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	poolConfig.MaxConns = int32(config.MaxConns)
	poolConfig.MinConns = int32(config.MinConns)
	poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = config.ConnMaxIdleTime

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresArtifactBackend{
		pool: pool,
	}, nil
}

func (b *PostgresArtifactBackend) Get(ctx context.Context, key string) ([]byte, error) {
	sessionID := extractSessionID(key)
	name := extractArtifactName(key)

	query := `SELECT data FROM artifacts WHERE session_id = $1 AND name = $2`

	var data []byte
	err := b.pool.QueryRow(ctx, query, sessionID, name).Scan(&data)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("artifact not found: %s", key)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact: %w", err)
	}

	return data, nil
}

func (b *PostgresArtifactBackend) Put(ctx context.Context, key string, data []byte) error {
	sessionID := extractSessionID(key)
	name := extractArtifactName(key)
	size := int64(len(data))

	query := `
		INSERT INTO artifacts (session_id, name, data, size, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (session_id, name) DO UPDATE
		SET data = EXCLUDED.data,
		    size = EXCLUDED.size,
		    updated_at = NOW()
	`

	_, err := b.pool.Exec(ctx, query, sessionID, name, data, size)
	if err != nil {
		return fmt.Errorf("failed to put artifact: %w", err)
	}

	return nil
}

func (b *PostgresArtifactBackend) Delete(ctx context.Context, key string) error {
	sessionID := extractSessionID(key)
	name := extractArtifactName(key)

	query := `DELETE FROM artifacts WHERE session_id = $1 AND name = $2`

	_, err := b.pool.Exec(ctx, query, sessionID, name)
	if err != nil {
		return fmt.Errorf("failed to delete artifact: %w", err)
	}

	return nil
}

func (b *PostgresArtifactBackend) List(ctx context.Context, prefix string) ([]string, error) {
	sessionID := prefix

	query := `SELECT name FROM artifacts WHERE session_id = $1 ORDER BY created_at DESC`

	rows, err := b.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		names = append(names, name)
	}

	return names, nil
}

func (b *PostgresArtifactBackend) Close() error {
	b.pool.Close()
	return nil
}

// PostgresMemoryBackend provides PostgreSQL-backed memory storage with pgvector
type PostgresMemoryBackend struct {
	pool *pgxpool.Pool
}

// NewPostgresMemoryBackend creates a new PostgreSQL memory backend
func NewPostgresMemoryBackend(config PostgresConfig) (*PostgresMemoryBackend, error) {
	if config.MaxConns == 0 {
		config.MaxConns = 20
	}
	if config.MinConns == 0 {
		config.MinConns = 5
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 1 * time.Hour
	}
	if config.ConnMaxIdleTime == 0 {
		config.ConnMaxIdleTime = 30 * time.Minute
	}
	if config.Port == 0 {
		config.Port = 5432
	}

	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=prefer",
		config.User, config.Password, config.Host, config.Port, config.Database,
	)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	poolConfig.MaxConns = int32(config.MaxConns)
	poolConfig.MinConns = int32(config.MinConns)
	poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = config.ConnMaxIdleTime

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresMemoryBackend{
		pool: pool,
	}, nil
}

func (b *PostgresMemoryBackend) Put(ctx context.Context, memory *runtime.Memory) error {
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}

	// Extract userID from metadata
	userID := ""
	if memory.Metadata != nil {
		if uid, ok := memory.Metadata["user_id"].(string); ok {
			userID = uid
		}
	}

	// Marshal metadata
	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Convert embedding to pgvector format (array string)
	embeddingStr := formatVectorForPostgres(memory.Embedding)

	query := `
		INSERT INTO memories (id, user_id, content, embedding, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET user_id = EXCLUDED.user_id,
		    content = EXCLUDED.content,
		    embedding = EXCLUDED.embedding,
		    metadata = EXCLUDED.metadata
	`

	_, err = b.pool.Exec(ctx, query,
		memory.ID,
		userID,
		memory.Content,
		embeddingStr,
		metadataJSON,
		memory.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to put memory: %w", err)
	}

	return nil
}

func (b *PostgresMemoryBackend) Get(ctx context.Context, memoryID string) (*runtime.Memory, error) {
	query := `
		SELECT id, user_id, content, embedding, metadata, created_at
		FROM memories
		WHERE id = $1
	`

	var memory runtime.Memory
	var userID string
	var embeddingStr string
	var metadataJSON []byte

	err := b.pool.QueryRow(ctx, query, memoryID).Scan(
		&memory.ID,
		&userID,
		&memory.Content,
		&embeddingStr,
		&metadataJSON,
		&memory.CreatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	// Parse embedding from pgvector format
	memory.Embedding = parseVectorFromPostgres(embeddingStr)

	// Unmarshal metadata
	if metadataJSON != nil {
		if err := json.Unmarshal(metadataJSON, &memory.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	// Set userID in metadata if not present
	if memory.Metadata == nil {
		memory.Metadata = make(map[string]interface{})
	}
	memory.Metadata["user_id"] = userID

	return &memory, nil
}

func (b *PostgresMemoryBackend) Delete(ctx context.Context, memoryID string) error {
	query := `DELETE FROM memories WHERE id = $1`

	_, err := b.pool.Exec(ctx, query, memoryID)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	return nil
}

func (b *PostgresMemoryBackend) List(ctx context.Context, userID string) ([]*runtime.Memory, error) {
	query := `
		SELECT id, user_id, content, embedding, metadata, created_at
		FROM memories
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := b.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	var memories []*runtime.Memory

	for rows.Next() {
		var memory runtime.Memory
		var uid string
		var embeddingStr string
		var metadataJSON []byte

		err := rows.Scan(
			&memory.ID,
			&uid,
			&memory.Content,
			&embeddingStr,
			&metadataJSON,
			&memory.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse embedding
		memory.Embedding = parseVectorFromPostgres(embeddingStr)

		// Unmarshal metadata
		if metadataJSON != nil {
			json.Unmarshal(metadataJSON, &memory.Metadata)
		}

		// Set userID in metadata
		if memory.Metadata == nil {
			memory.Metadata = make(map[string]interface{})
		}
		memory.Metadata["user_id"] = uid

		memories = append(memories, &memory)
	}

	return memories, nil
}

func (b *PostgresMemoryBackend) Close() error {
	b.pool.Close()
	return nil
}

// Helper functions for pgvector format conversion

func formatVectorForPostgres(embedding []float64) string {
	// pgvector expects format: "[0.1, 0.2, 0.3]"
	if len(embedding) == 0 {
		return "[]"
	}

	result := "["
	for i, val := range embedding {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("%f", val)
	}
	result += "]"
	return result
}

func parseVectorFromPostgres(vectorStr string) []float64 {
	// Parse pgvector string format "[0.1, 0.2, 0.3]"
	if vectorStr == "" || vectorStr == "[]" {
		return []float64{}
	}

	// Simple parsing (production code should be more robust)
	var embedding []float64
	var current float64
	var hasValue bool

	for i := 0; i < len(vectorStr); i++ {
		c := vectorStr[i]
		if c >= '0' && c <= '9' || c == '.' || c == '-' || c == 'e' || c == 'E' || c == '+' {
			// Parse number
			start := i
			for i < len(vectorStr) && (vectorStr[i] >= '0' && vectorStr[i] <= '9' || vectorStr[i] == '.' || vectorStr[i] == '-' || vectorStr[i] == 'e' || vectorStr[i] == 'E' || vectorStr[i] == '+') {
				i++
			}
			fmt.Sscanf(vectorStr[start:i], "%f", &current)
			hasValue = true
			i--
		} else if c == ',' || c == ']' {
			if hasValue {
				embedding = append(embedding, current)
				hasValue = false
			}
		}
	}

	return embedding
}
