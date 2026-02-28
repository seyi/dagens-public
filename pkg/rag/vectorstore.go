// Package rag provides Retrieval-Augmented Generation (RAG) capabilities
// for agents, with vendor-neutral vector database abstractions
package rag

import (
	"context"
	"time"
)

// VectorStore is a vendor-neutral abstraction for vector databases
// Implementations can be provided for Weaviate, Pinecone, Milvus, Qdrant, etc.
type VectorStore interface {
	// Store stores a document with its embedding in the vector database
	Store(ctx context.Context, doc *Document) error

	// StoreBatch stores multiple documents efficiently
	StoreBatch(ctx context.Context, docs []*Document) error

	// Search performs similarity search and returns top-k documents
	Search(ctx context.Context, query *SearchQuery) ([]*SearchResult, error)

	// Delete removes a document by ID
	Delete(ctx context.Context, docID string) error

	// DeleteBatch removes multiple documents
	DeleteBatch(ctx context.Context, docIDs []string) error

	// GetDocument retrieves a document by ID
	GetDocument(ctx context.Context, docID string) (*Document, error)

	// ListDocuments lists documents with pagination
	ListDocuments(ctx context.Context, opts *ListOptions) ([]*Document, error)

	// CreateCollection creates a new collection/index
	CreateCollection(ctx context.Context, config *CollectionConfig) error

	// DeleteCollection deletes a collection/index
	DeleteCollection(ctx context.Context, collectionName string) error

	// Close closes the connection to the vector database
	Close() error

	// HealthCheck verifies the vector database is accessible
	HealthCheck(ctx context.Context) error
}

// Document represents a document stored in the vector database
type Document struct {
	ID         string                 // Unique identifier
	Content    string                 // Text content
	Embedding  []float64              // Vector embedding
	Metadata   map[string]interface{} // Additional metadata
	Collection string                 // Collection/namespace name
	Source     string                 // Source file/URL
	CreatedAt  time.Time              // Creation timestamp
	UpdatedAt  time.Time              // Last update timestamp
}

// SearchQuery defines parameters for similarity search
type SearchQuery struct {
	// QueryText is the search query (will be embedded if Embedding not provided)
	QueryText string

	// Embedding is the pre-computed query embedding (optional)
	Embedding []float64

	// Collection to search in
	Collection string

	// TopK is the number of results to return
	TopK int

	// MinScore is the minimum similarity score threshold (0.0 to 1.0)
	MinScore float64

	// MetadataFilter allows filtering by metadata fields
	MetadataFilter map[string]interface{}

	// IncludeEmbeddings whether to include embeddings in results (default: false)
	IncludeEmbeddings bool
}

// SearchResult represents a search result with similarity score
type SearchResult struct {
	Document   *Document // The matched document
	Score      float64   // Similarity score (0.0 to 1.0, higher is better)
	Distance   float64   // Distance metric (lower is better)
	Highlights []string  // Text highlights (optional)
}

// ListOptions defines pagination and filtering for listing documents
type ListOptions struct {
	Collection string // Collection to list from
	Offset     int    // Number of documents to skip
	Limit      int    // Maximum number of documents to return
	SortBy     string // Field to sort by
	Ascending  bool   // Sort direction
}

// CollectionConfig defines configuration for creating a collection
type CollectionConfig struct {
	Name              string // Collection name
	VectorDimensions  int    // Embedding vector dimensions
	DistanceMetric    string // cosine, euclidean, dot
	Description       string // Collection description
	Properties        map[string]PropertyConfig
	ReplicationFactor int // Number of replicas (if supported)
}

// PropertyConfig defines metadata property configuration
type PropertyConfig struct {
	Name        string
	Type        string // string, int, float, bool, date
	Description string
	Indexed     bool // Whether to index this property
}

// VectorStoreConfig contains common configuration for vector stores
type VectorStoreConfig struct {
	Host              string        // Vector DB host
	Port              int           // Vector DB port
	APIKey            string        // API key (if required)
	Timeout           time.Duration // Request timeout
	MaxRetries        int           // Maximum retry attempts
	DefaultCollection string        // Default collection name
	BatchSize         int           // Batch size for bulk operations
}

// DistanceMetric constants
const (
	DistanceMetricCosine    = "cosine"
	DistanceMetricEuclidean = "euclidean"
	DistanceMetricDot       = "dot"
)

// PropertyType constants
const (
	PropertyTypeString = "string"
	PropertyTypeInt    = "int"
	PropertyTypeFloat  = "float"
	PropertyTypeBool   = "bool"
	PropertyTypeDate   = "date"
)

// DefaultSearchQuery returns a search query with sensible defaults
func DefaultSearchQuery(queryText string) *SearchQuery {
	return &SearchQuery{
		QueryText:         queryText,
		TopK:              10,
		MinScore:          0.0,
		IncludeEmbeddings: false,
	}
}

// DefaultCollectionConfig returns collection config with sensible defaults
func DefaultCollectionConfig(name string, vectorDims int) *CollectionConfig {
	return &CollectionConfig{
		Name:              name,
		VectorDimensions:  vectorDims,
		DistanceMetric:    DistanceMetricCosine,
		ReplicationFactor: 1,
		Properties:        make(map[string]PropertyConfig),
	}
}
