package tools

import (
	"context"
	"fmt"

	// "github.com/seyi/dagens/pkg/rag"  // Temporarily commented out due to missing Corpus type
	"github.com/seyi/dagens/pkg/types"
)

// RagRetrievalConfig configures the RAG retrieval tool
type RagRetrievalConfig struct {
	// Temporarily using a generic interface{} until Corpus type is properly defined
	SearchFunc func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error)
}

// RagRetrievalTool creates a tool for RAG retrieval
func RagRetrievalTool(config RagRetrievalConfig) *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "rag_retrieval",
		Description: "Retrieves relevant documents from a corpus",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type": "string",
					},
					"top_k": map[string]interface{}{
						"type": "integer",
					},
					"min_score": map[string]interface{}{
						"type": "number",
					},
				},
				"required": []string{"query"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			query, ok := params["query"].(string)
			if !ok {
				return nil, fmt.Errorf("query parameter is required")
			}
			topK, _ := params["top_k"].(int)
			minScore, _ := params["min_score"].(float64)

			if config.SearchFunc == nil {
				return nil, fmt.Errorf("search function not configured")
			}

			results, err := config.SearchFunc(ctx, query, topK, minScore)
			if err != nil {
				return nil, err
			}
			return results, nil
		},
		Enabled: true,
	}
}

// SimpleRagRetrievalTool creates a simple RAG retrieval tool
func SimpleRagRetrievalTool() *types.ToolDefinition {
	// Return a tool with a mock search function for now
	config := RagRetrievalConfig{
		SearchFunc: func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error) {
			return []map[string]interface{}{
				{
					"content": "Mock search result for: " + query,
					"score":   0.9,
					"source":  "mock",
				},
			}, nil
		},
	}
	return RagRetrievalTool(config)
}

// RegisterRagTools registers the RAG tools
func RegisterRagTools(registry *ToolRegistry, searchFunc func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error)) error {
	config := RagRetrievalConfig{SearchFunc: searchFunc}
	return registry.Register(RagRetrievalTool(config))
}
