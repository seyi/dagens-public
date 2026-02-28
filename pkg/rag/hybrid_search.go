package rag

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
)

// HybridSearchQuery combines vector and keyword search
type HybridSearchQuery struct {
	// QueryText for both semantic and keyword search
	QueryText string

	// Embedding for semantic search (optional, will be generated if not provided)
	Embedding []float64

	// Collection to search in
	Collection string

	// TopK results to return
	TopK int

	// MinScore threshold (0.0 to 1.0)
	MinScore float64

	// VectorWeight determines semantic vs keyword balance (0.0 = keyword only, 1.0 = semantic only)
	// Default: 0.7 (70% semantic, 30% keyword)
	VectorWeight float64

	// KeywordWeight is the complement of VectorWeight (calculated automatically)
	KeywordWeight float64

	// MetadataFilter for filtering by metadata
	MetadataFilter map[string]interface{}

	// EnableReranking enables cross-encoder re-ranking (optional)
	EnableReranking bool

	// RerankTopK number of results to re-rank (default: TopK * 2)
	RerankTopK int
}

// HybridSearchResult contains both vector and keyword scores
type HybridSearchResult struct {
	Document *Document

	// Scoring
	VectorScore  float64 // Semantic similarity score (0.0 to 1.0)
	KeywordScore float64 // BM25 keyword score (normalized 0.0 to 1.0)
	HybridScore  float64 // Combined score
	RerankScore  float64 // Re-ranking score (if enabled)

	// Grounding information
	GroundingSpans []GroundingSpan // Text spans that match the query
	Confidence     float64         // Confidence in this result (0.0 to 1.0)

	// Metadata
	MatchType string // "semantic", "keyword", or "hybrid"
}

// GroundingSpan represents a text span that grounds the answer
type GroundingSpan struct {
	Text       string  // The actual text span
	StartChar  int     // Start character position in document
	EndChar    int     // End character position in document
	Score      float64 // Relevance score for this span
	MatchType  string  // "semantic", "keyword", or "exact"
	MatchTerms []string // Matched query terms (for keyword matches)
}

// HybridVectorStore extends VectorStore with hybrid search capabilities
type HybridVectorStore interface {
	VectorStore

	// HybridSearch performs combined vector + keyword search
	HybridSearch(ctx context.Context, query *HybridSearchQuery) ([]*HybridSearchResult, error)

	// BuildInvertedIndex builds keyword index for BM25 search
	BuildInvertedIndex(ctx context.Context, collection string) error
}

// BM25Index implements BM25 keyword scoring
type BM25Index struct {
	// Document frequency: term -> number of documents containing term
	docFreq map[string]int

	// Term frequency per document: docID -> (term -> count)
	termFreq map[string]map[string]int

	// Document lengths: docID -> length
	docLengths map[string]int

	// Average document length
	avgDocLength float64

	// Total number of documents
	numDocs int

	// BM25 parameters
	k1 float64 // Term frequency saturation (default: 1.5)
	b  float64 // Length normalization (default: 0.75)

	mu sync.RWMutex
}

// NewBM25Index creates a new BM25 index
func NewBM25Index() *BM25Index {
	return &BM25Index{
		docFreq:    make(map[string]int),
		termFreq:   make(map[string]map[string]int),
		docLengths: make(map[string]int),
		k1:         1.5,
		b:          0.75,
	}
}

// IndexDocument adds a document to the BM25 index
func (idx *BM25Index) IndexDocument(docID string, text string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Tokenize
	tokens := tokenize(text)

	// Count term frequencies
	termCounts := make(map[string]int)
	for _, token := range tokens {
		termCounts[token]++
	}

	// Update index
	idx.termFreq[docID] = termCounts
	idx.docLengths[docID] = len(tokens)
	idx.numDocs++

	// Update document frequencies
	for term := range termCounts {
		idx.docFreq[term]++
	}

	// Recalculate average document length
	totalLength := 0
	for _, length := range idx.docLengths {
		totalLength += length
	}
	if idx.numDocs > 0 {
		idx.avgDocLength = float64(totalLength) / float64(idx.numDocs)
	}
}

// Score calculates BM25 score for a document given a query
func (idx *BM25Index) Score(docID string, queryTerms []string) float64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	score := 0.0
	docLength := float64(idx.docLengths[docID])

	for _, term := range queryTerms {
		// Get term frequency in document
		tf := float64(idx.termFreq[docID][term])
		if tf == 0 {
			continue
		}

		// Get document frequency
		df := float64(idx.docFreq[term])
		if df == 0 {
			continue
		}

		// Calculate IDF (inverse document frequency)
		// Using log((N - df + 0.5) / (df + 0.5) + 1) to ensure positive values
		idf := math.Log(1 + (float64(idx.numDocs)-df+0.5)/(df+0.5))

		// Calculate BM25 score for this term
		numerator := tf * (idx.k1 + 1)
		denominator := tf + idx.k1*(1-idx.b+idx.b*(docLength/idx.avgDocLength))

		score += idf * (numerator / denominator)
	}

	return score
}

// GetMatchingTerms returns query terms that appear in the document
func (idx *BM25Index) GetMatchingTerms(docID string, queryTerms []string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	matching := make([]string, 0)
	for _, term := range queryTerms {
		if idx.termFreq[docID][term] > 0 {
			matching = append(matching, term)
		}
	}
	return matching
}

// ReRanker re-ranks search results for better relevance
type ReRanker interface {
	// Rerank re-ranks results based on query-document relevance
	Rerank(ctx context.Context, query string, results []*HybridSearchResult) ([]*HybridSearchResult, error)
}

// SimpleReRanker implements basic re-ranking based on exact matches
type SimpleReRanker struct{}

// NewSimpleReRanker creates a simple re-ranker
func NewSimpleReRanker() *SimpleReRanker {
	return &SimpleReRanker{}
}

func (r *SimpleReRanker) Rerank(ctx context.Context, query string, results []*HybridSearchResult) ([]*HybridSearchResult, error) {
	queryLower := strings.ToLower(query)

	for _, result := range results {
		contentLower := strings.ToLower(result.Document.Content)

		// Boost score for exact phrase matches
		if strings.Contains(contentLower, queryLower) {
			result.RerankScore = result.HybridScore * 1.5
		} else {
			// Check for partial matches
			queryWords := strings.Fields(queryLower)
			matchCount := 0
			for _, word := range queryWords {
				if strings.Contains(contentLower, word) {
					matchCount++
				}
			}
			matchRatio := float64(matchCount) / float64(len(queryWords))
			result.RerankScore = result.HybridScore * (1.0 + matchRatio*0.3)
		}
	}

	// Sort by rerank score
	sort.Slice(results, func(i, j int) bool {
		return results[i].RerankScore > results[j].RerankScore
	})

	return results, nil
}

// ExtractGroundingSpans extracts relevant text spans from a document
func ExtractGroundingSpans(doc *Document, query string, matchingTerms []string, maxSpans int) []GroundingSpan {
	spans := make([]GroundingSpan, 0)
	contentLower := strings.ToLower(doc.Content)
	queryLower := strings.ToLower(query)

	// Look for exact phrase matches first
	if idx := strings.Index(contentLower, queryLower); idx != -1 {
		endIdx := idx + len(query)
		spans = append(spans, GroundingSpan{
			Text:      doc.Content[idx:endIdx],
			StartChar: idx,
			EndChar:   endIdx,
			Score:     1.0,
			MatchType: "exact",
		})
	}

	// Look for keyword matches
	for _, term := range matchingTerms {
		termLower := strings.ToLower(term)
		startPos := 0

		for {
			idx := strings.Index(contentLower[startPos:], termLower)
			if idx == -1 {
				break
			}

			actualIdx := startPos + idx
			endIdx := actualIdx + len(term)

			// Extract context around the match (±50 chars)
			contextStart := actualIdx - 50
			if contextStart < 0 {
				contextStart = 0
			}
			contextEnd := endIdx + 50
			if contextEnd > len(doc.Content) {
				contextEnd = len(doc.Content)
			}

			spans = append(spans, GroundingSpan{
				Text:       doc.Content[contextStart:contextEnd],
				StartChar:  contextStart,
				EndChar:    contextEnd,
				Score:      0.7,
				MatchType:  "keyword",
				MatchTerms: []string{term},
			})

			startPos = endIdx
			if len(spans) >= maxSpans {
				break
			}
		}

		if len(spans) >= maxSpans {
			break
		}
	}

	// Sort by score
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Score > spans[j].Score
	})

	// Limit to maxSpans
	if len(spans) > maxSpans {
		spans = spans[:maxSpans]
	}

	return spans
}

// Helper functions

func tokenize(text string) []string {
	// Simple tokenization (can be enhanced with NLP libraries)
	text = strings.ToLower(text)

	// Replace punctuation with spaces
	replacer := strings.NewReplacer(
		".", " ",
		",", " ",
		"!", " ",
		"?", " ",
		";", " ",
		":", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"\"", " ",
		"'", " ",
	)
	text = replacer.Replace(text)

	// Split and filter
	tokens := strings.Fields(text)

	// Remove stop words (simplified list)
	stopWords := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true,
		"at": true, "be": true, "by": true, "for": true, "from": true,
		"has": true, "he": true, "in": true, "is": true, "it": true,
		"its": true, "of": true, "on": true, "that": true, "the": true,
		"to": true, "was": true, "will": true, "with": true,
	}

	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) > 2 && !stopWords[token] {
			filtered = append(filtered, token)
		}
	}

	return filtered
}

// NormalizeBM25Score normalizes BM25 score to 0.0-1.0 range
func NormalizeBM25Score(score float64, maxScore float64) float64 {
	if maxScore == 0 {
		return 0.0
	}
	normalized := score / maxScore
	if normalized > 1.0 {
		normalized = 1.0
	}
	return normalized
}

// DefaultHybridSearchQuery returns a hybrid search query with sensible defaults
func DefaultHybridSearchQuery(queryText string) *HybridSearchQuery {
	return &HybridSearchQuery{
		QueryText:       queryText,
		TopK:            10,
		MinScore:        0.0,
		VectorWeight:    0.7,  // 70% semantic
		KeywordWeight:   0.3,  // 30% keyword
		EnableReranking: false,
		RerankTopK:      20,
	}
}
