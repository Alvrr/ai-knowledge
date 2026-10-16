package embedding

import (
	"context"

	"github.com/pgvector/pgvector-go"
)

// ReferenceContext represents reference document data retrieved from vector search.
type ReferenceContext struct {
	ID         int     `json:"id"`
	Category   string  `json:"category"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}

// FeedbackResult represents the structured AI feedback response.
type FeedbackResult struct {
	RelatedService    string  `json:"related_service"`
	Category          string  `json:"category"`
	ShortDescription  string  `json:"short_description"`
	LongSummary       string  `json:"long_summary"`
	BestSimilarity    float64 `json:"best_similarity"`
	BestSimilarityPct string  `json:"best_similarity_percentage"`
}

// Embedder defines the interface for document and query vector generation,
// as well as AI feedback generation using Google Gemini Embedding 2.
type Embedder interface {
	GenerateDocumentEmbedding(ctx context.Context, title, content string) (pgvector.Vector, error)
	GenerateQueryEmbedding(ctx context.Context, query string) (pgvector.Vector, error)
	GenerateEmbedding(ctx context.Context, text string) (pgvector.Vector, error)
	GenerateFeedback(ctx context.Context, userQuery string, refs []ReferenceContext) (*FeedbackResult, error)
	GetDimension() int
	GetModelName() string
	Close()
}
