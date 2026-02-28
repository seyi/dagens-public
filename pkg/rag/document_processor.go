package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DocumentProcessor handles document chunking and embedding
type DocumentProcessor struct {
	chunker         TextChunker
	embedder        EmbeddingService
	vectorStore     VectorStore
	defaultCollection string
}

// NewDocumentProcessor creates a new document processor
func NewDocumentProcessor(
	chunker TextChunker,
	embedder EmbeddingService,
	vectorStore VectorStore,
	defaultCollection string,
) *DocumentProcessor {
	return &DocumentProcessor{
		chunker:         chunker,
		embedder:        embedder,
		vectorStore:     vectorStore,
		defaultCollection: defaultCollection,
	}
}

// ProcessDocument processes a document (chunks, embeds, stores)
func (dp *DocumentProcessor) ProcessDocument(ctx context.Context, content string, metadata map[string]interface{}) error {
	return dp.ProcessDocumentWithID(ctx, uuid.New().String(), content, metadata)
}

// ProcessDocumentWithID processes a document with a specific ID
func (dp *DocumentProcessor) ProcessDocumentWithID(ctx context.Context, docID string, content string, metadata map[string]interface{}) error {
	// Chunk the document
	chunks := dp.chunker.Chunk(content)

	// Generate embeddings for chunks
	embeddings, err := dp.embedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Create documents for each chunk
	documents := make([]*Document, len(chunks))
	collection := dp.defaultCollection
	if coll, ok := metadata["collection"].(string); ok {
		collection = coll
	}

	source := ""
	if src, ok := metadata["source"].(string); ok {
		source = src
	}

	now := time.Now()
	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("%s_chunk_%d", docID, i)

		// Copy metadata and add chunk-specific info
		chunkMetadata := make(map[string]interface{})
		for k, v := range metadata {
			chunkMetadata[k] = v
		}
		chunkMetadata["chunk_index"] = i
		chunkMetadata["total_chunks"] = len(chunks)
		chunkMetadata["parent_doc_id"] = docID

		documents[i] = &Document{
			ID:         chunkID,
			Content:    chunk,
			Embedding:  embeddings[i],
			Metadata:   chunkMetadata,
			Collection: collection,
			Source:     source,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
	}

	// Store in vector database
	return dp.vectorStore.StoreBatch(ctx, documents)
}

// ProcessDocuments processes multiple documents in batch
func (dp *DocumentProcessor) ProcessDocuments(ctx context.Context, contents []string, metadataList []map[string]interface{}) error {
	if len(contents) != len(metadataList) {
		return fmt.Errorf("contents and metadata lists must have same length")
	}

	for i, content := range contents {
		if err := dp.ProcessDocument(ctx, content, metadataList[i]); err != nil {
			return fmt.Errorf("failed to process document %d: %w", i, err)
		}
	}

	return nil
}

// TextChunker splits text into chunks
type TextChunker interface {
	// Chunk splits text into chunks
	Chunk(text string) []string

	// ChunkSize returns the maximum chunk size
	ChunkSize() int
}

// FixedSizeChunker splits text into fixed-size chunks with overlap
type FixedSizeChunker struct {
	chunkSize int
	overlap   int
}

// NewFixedSizeChunker creates a fixed-size chunker
func NewFixedSizeChunker(chunkSize, overlap int) *FixedSizeChunker {
	return &FixedSizeChunker{
		chunkSize: chunkSize,
		overlap:   overlap,
	}
}

func (c *FixedSizeChunker) Chunk(text string) []string {
	if len(text) == 0 {
		return []string{}
	}

	chunks := make([]string, 0)
	start := 0
	lastStart := -1

	for start < len(text) {
		// Prevent infinite loop
		if start == lastStart {
			break
		}
		lastStart = start

		end := start + c.chunkSize
		if end > len(text) {
			end = len(text)
		}

		chunk := text[start:end]
		chunks = append(chunks, chunk)

		// Move start position with overlap
		start += c.chunkSize - c.overlap

		// If overlap >= chunkSize, we won't make progress
		if c.overlap >= c.chunkSize {
			break
		}
	}

	return chunks
}

func (c *FixedSizeChunker) ChunkSize() int {
	return c.chunkSize
}

// SentenceChunker splits text by sentences with maximum chunk size
type SentenceChunker struct {
	maxChunkSize int
}

// NewSentenceChunker creates a sentence-based chunker
func NewSentenceChunker(maxChunkSize int) *SentenceChunker {
	return &SentenceChunker{
		maxChunkSize: maxChunkSize,
	}
}

func (c *SentenceChunker) Chunk(text string) []string {
	// Split by sentence delimiters
	sentences := c.splitSentences(text)

	chunks := make([]string, 0)
	currentChunk := ""

	for _, sentence := range sentences {
		// If adding this sentence exceeds max size, start new chunk
		if len(currentChunk)+len(sentence) > c.maxChunkSize && currentChunk != "" {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
			currentChunk = ""
		}

		currentChunk += sentence + " "

		// If single sentence exceeds max size, split it
		if len(currentChunk) > c.maxChunkSize {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
			currentChunk = ""
		}
	}

	// Add remaining chunk
	if currentChunk != "" {
		chunks = append(chunks, strings.TrimSpace(currentChunk))
	}

	return chunks
}

func (c *SentenceChunker) ChunkSize() int {
	return c.maxChunkSize
}

func (c *SentenceChunker) splitSentences(text string) []string {
	// Simple sentence splitting (can be improved with NLP libraries)
	text = strings.ReplaceAll(text, ".\n", ".|")
	text = strings.ReplaceAll(text, ". ", ".|")
	text = strings.ReplaceAll(text, "! ", "!|")
	text = strings.ReplaceAll(text, "? ", "?|")

	sentences := strings.Split(text, "|")
	result := make([]string, 0)

	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s != "" {
			result = append(result, s)
		}
	}

	return result
}

// ParagraphChunker splits text by paragraphs with maximum chunk size
type ParagraphChunker struct {
	maxChunkSize int
}

// NewParagraphChunker creates a paragraph-based chunker
func NewParagraphChunker(maxChunkSize int) *ParagraphChunker {
	return &ParagraphChunker{
		maxChunkSize: maxChunkSize,
	}
}

func (c *ParagraphChunker) Chunk(text string) []string {
	// Split by paragraphs (double newline)
	paragraphs := strings.Split(text, "\n\n")

	chunks := make([]string, 0)
	currentChunk := ""

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// If adding this paragraph exceeds max size, start new chunk
		if len(currentChunk)+len(para) > c.maxChunkSize && currentChunk != "" {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
			currentChunk = ""
		}

		currentChunk += para + "\n\n"

		// If single paragraph exceeds max size, split by sentences
		if len(currentChunk) > c.maxChunkSize {
			sentenceChunker := NewSentenceChunker(c.maxChunkSize)
			subChunks := sentenceChunker.Chunk(currentChunk)
			chunks = append(chunks, subChunks...)
			currentChunk = ""
		}
	}

	// Add remaining chunk
	if currentChunk != "" {
		chunks = append(chunks, strings.TrimSpace(currentChunk))
	}

	return chunks
}

func (c *ParagraphChunker) ChunkSize() int {
	return c.maxChunkSize
}

// DefaultChunker returns a sensible default chunker
func DefaultChunker() TextChunker {
	// Paragraph-based chunking with 1000 character max
	// Good balance between context and embedding quality
	return NewParagraphChunker(1000)
}
