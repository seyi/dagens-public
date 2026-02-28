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

package database

import (
	"context"
	"fmt"
	"time"
)

// QueryResultStream represents a streaming query result.
// Used for processing large result sets without loading all data into memory.
type QueryResultStream struct {
	// Columns is the list of column names
	Columns []string

	// Rows is a channel that streams result rows
	Rows <-chan map[string]interface{}

	// Errors is a channel for errors during streaming
	Errors <-chan error

	// Metadata contains query metadata
	Metadata *StreamMetadata

	// Cancel can be called to stop streaming early
	Cancel context.CancelFunc
}

// StreamMetadata contains metadata about the streaming query.
type StreamMetadata struct {
	Query         string
	StartTime     time.Time
	RowsStreamed  int64
	BytesStreamed int64
	Completed     bool
	CompletedAt   *time.Time
}

// StreamConfig configures streaming behavior.
type StreamConfig struct {
	// BufferSize is the channel buffer size (default: 100)
	BufferSize int

	// BatchSize is the number of rows to fetch at once (default: 1000)
	BatchSize int

	// MaxRows limits the total rows streamed (0 = unlimited)
	MaxRows int64

	// Timeout for the entire streaming operation
	Timeout time.Duration
}

// DefaultStreamConfig returns default streaming configuration.
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		BufferSize: 100,
		BatchSize:  1000,
		MaxRows:    0, // unlimited
		Timeout:    30 * time.Minute,
	}
}

// StreamQuery executes a SQL query and streams results via channel.
// This is memory-efficient for large result sets.
//
// Example usage:
//
//	stream, err := session.StreamQuery(ctx, "SELECT * FROM large_table")
//	if err != nil {
//	    return err
//	}
//	defer stream.Cancel()
//
//	for row := range stream.Rows {
//	    // Process row
//	    fmt.Printf("Row: %v\n", row)
//	}
//
//	// Check for errors
//	if err := <-stream.Errors; err != nil {
//	    return err
//	}
func (s *SparkSession) StreamQuery(ctx context.Context, query string, config *StreamConfig) (*QueryResultStream, error) {
	s.mu.RLock()
	if !s.connected || s.db == nil {
		s.mu.RUnlock()
		return nil, fmt.Errorf("not connected to database")
	}
	db := s.db
	s.mu.RUnlock()

	if config == nil {
		config = DefaultStreamConfig()
	}

	// Create streaming context with timeout
	streamCtx, cancel := context.WithCancel(ctx)
	if config.Timeout > 0 {
		streamCtx, cancel = context.WithTimeout(ctx, config.Timeout)
	}

	// Execute query
	rows, err := db.QueryContext(streamCtx, query)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("query execution failed: %w", err)
	}

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		cancel()
		rows.Close()
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Create result stream
	rowsChan := make(chan map[string]interface{}, config.BufferSize)
	errorsChan := make(chan error, 1)

	metadata := &StreamMetadata{
		Query:     query,
		StartTime: time.Now(),
	}

	stream := &QueryResultStream{
		Columns:  columns,
		Rows:     rowsChan,
		Errors:   errorsChan,
		Metadata: metadata,
		Cancel:   cancel,
	}

	// Start streaming in background
	go func() {
		defer close(rowsChan)
		defer close(errorsChan)
		defer rows.Close()
		defer cancel()

		var rowCount int64 = 0

		for rows.Next() {
			// Check cancellation
			select {
			case <-streamCtx.Done():
				errorsChan <- streamCtx.Err()
				return
			default:
			}

			// Check MaxRows limit
			if config.MaxRows > 0 && rowCount >= config.MaxRows {
				break
			}

			// Create value holders
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			// Scan row
			if err := rows.Scan(valuePtrs...); err != nil {
				errorsChan <- fmt.Errorf("failed to scan row: %w", err)
				return
			}

			// Convert to map
			rowMap := make(map[string]interface{})
			for i, col := range columns {
				val := values[i]

				// Convert []byte to string for readability
				if b, ok := val.([]byte); ok {
					rowMap[col] = string(b)
				} else {
					rowMap[col] = val
				}
			}

			// Send row to channel
			select {
			case rowsChan <- rowMap:
				rowCount++
				metadata.RowsStreamed = rowCount
			case <-streamCtx.Done():
				errorsChan <- streamCtx.Err()
				return
			}
		}

		// Check for errors during iteration
		if err := rows.Err(); err != nil {
			errorsChan <- fmt.Errorf("error during row iteration: %w", err)
			return
		}

		// Mark as completed
		now := time.Now()
		metadata.Completed = true
		metadata.CompletedAt = &now

		// Update session stats
		s.mu.Lock()
		s.stats.QueriesExecuted++
		s.stats.TotalTime += time.Since(metadata.StartTime)
		s.mu.Unlock()

		// No error
		errorsChan <- nil
	}()

	return stream, nil
}

// QueryResultPage represents a paginated query result.
// Alternative to streaming for when you want discrete pages.
type QueryResultPage struct {
	// Columns is the list of column names
	Columns []string

	// Rows contains the current page of data
	Rows []map[string]interface{}

	// RowCount is the number of rows in this page
	RowCount int

	// Page is the current page number (1-based)
	Page int

	// PageSize is the size of each page
	PageSize int

	// HasNextPage indicates if more pages are available
	HasNextPage bool

	// TotalRows is the total count if known (-1 if unknown)
	TotalRows int64

	// ExecutionTime for fetching this page
	ExecutionTime float64

	// Query that was executed
	Query string
}

// PaginationConfig configures pagination behavior.
type PaginationConfig struct {
	// PageSize is the number of rows per page (default: 1000)
	PageSize int

	// Page is the page number to fetch (1-based)
	Page int

	// IncludeTotalCount whether to execute COUNT(*) query (can be expensive)
	IncludeTotalCount bool
}

// DefaultPaginationConfig returns default pagination configuration.
func DefaultPaginationConfig() *PaginationConfig {
	return &PaginationConfig{
		PageSize:          1000,
		Page:              1,
		IncludeTotalCount: false,
	}
}

// ExecuteQueryPaginated executes a SQL query and returns a paginated result.
// This is useful when you want discrete pages instead of streaming.
//
// Example usage:
//
//	config := &PaginationConfig{PageSize: 100, Page: 1}
//	page, err := session.ExecuteQueryPaginated(ctx, "SELECT * FROM users", config)
//	if page.HasNextPage {
//	    // Fetch next page
//	    nextPage, _ := session.ExecuteQueryPaginated(ctx, query, &PaginationConfig{Page: 2})
//	}
func (s *SparkSession) ExecuteQueryPaginated(ctx context.Context, query string, config *PaginationConfig) (*QueryResultPage, error) {
	s.mu.RLock()
	if !s.connected || s.db == nil {
		s.mu.RUnlock()
		return nil, fmt.Errorf("not connected to database")
	}
	db := s.db
	s.mu.RUnlock()

	if config == nil {
		config = DefaultPaginationConfig()
	}

	if config.Page < 1 {
		return nil, fmt.Errorf("page must be >= 1")
	}

	startTime := time.Now()

	// Calculate offset
	offset := (config.Page - 1) * config.PageSize
	limit := config.PageSize + 1 // Fetch one extra to check for next page

	// Build paginated query
	paginatedQuery := fmt.Sprintf("%s LIMIT %d OFFSET %d", query, limit, offset)

	// Execute query
	rows, err := db.QueryContext(ctx, paginatedQuery)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Read rows
	results := make([]map[string]interface{}, 0, config.PageSize)
	rowCount := 0

	for rows.Next() && rowCount < limit {
		// Create value holders
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan row
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert to map
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]

			// Convert []byte to string for readability
			if b, ok := val.([]byte); ok {
				rowMap[col] = string(b)
			} else {
				rowMap[col] = val
			}
		}

		results = append(results, rowMap)
		rowCount++
	}

	// Check for errors
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	// Determine if there's a next page
	hasNextPage := rowCount > config.PageSize
	if hasNextPage {
		// Remove the extra row we fetched
		results = results[:config.PageSize]
		rowCount = config.PageSize
	}

	executionTime := time.Since(startTime)

	// Get total count if requested
	var totalRows int64 = -1
	if config.IncludeTotalCount {
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS count_query", query)
		err := db.QueryRowContext(ctx, countQuery).Scan(&totalRows)
		if err != nil {
			// Don't fail on count error, just leave it as -1
			totalRows = -1
		}
	}

	// Update session stats
	s.mu.Lock()
	s.stats.QueriesExecuted++
	s.stats.TotalTime += executionTime
	s.mu.Unlock()

	// Build page result
	page := &QueryResultPage{
		Columns:       columns,
		Rows:          results,
		RowCount:      rowCount,
		Page:          config.Page,
		PageSize:      config.PageSize,
		HasNextPage:   hasNextPage,
		TotalRows:     totalRows,
		ExecutionTime: executionTime.Seconds(),
		Query:         paginatedQuery,
	}

	return page, nil
}

// CollectStream collects all rows from a stream into a QueryResult.
// Useful for converting streaming results back to standard results.
// WARNING: This loads all data into memory - defeats the purpose of streaming!
func CollectStream(stream *QueryResultStream) (*QueryResult, error) {
	rows := make([]map[string]interface{}, 0)

	for row := range stream.Rows {
		rows = append(rows, row)
	}

	// Check for errors
	err := <-stream.Errors
	if err != nil {
		return nil, err
	}

	executionTime := 0.0
	if stream.Metadata.CompletedAt != nil {
		executionTime = stream.Metadata.CompletedAt.Sub(stream.Metadata.StartTime).Seconds()
	}

	return &QueryResult{
		Columns:       stream.Columns,
		Rows:          rows,
		RowCount:      len(rows),
		ExecutionTime: executionTime,
		Query:         stream.Metadata.Query,
		Cached:        false,
	}, nil
}
