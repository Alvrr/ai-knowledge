package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"go-pgvector-gemini/db"
	"go-pgvector-gemini/embedding"
)

type SearchRequest struct {
	Query      string `json:"query"`
	Limit      int    `json:"limit"`
	Collection string `json:"collection,omitempty"`
}

type MatchedSource struct {
	ID            int     `json:"id"`
	Category      string  `json:"category"`
	Title         string  `json:"title"`
	Similarity    float64 `json:"similarity"`
	SimilarityPct string  `json:"similarity_percentage"`
}

type SearchExecutionResult struct {
	Status          string                    `json:"status"`
	Collection      string                    `json:"collection"`
	Model           string                    `json:"model"`
	Source          string                    `json:"source"`
	CacheHit        bool                      `json:"cache_hit"`
	CacheSimilarity string                    `json:"cache_similarity,omitempty"`
	MatchedHistory  string                    `json:"matched_history,omitempty"`
	Query           string                    `json:"query"`
	Feedback        *embedding.FeedbackResult `json:"feedback"`
	MatchedSources  []MatchedSource           `json:"matched_sources"`
}

// ExecuteSearch performs search against the specified collection using Gemini 2 embedder.
func (c *DocumentController) ExecuteSearch(ctx context.Context, collection, queryText string, limit int) (*SearchExecutionResult, error) {
	if limit <= 0 {
		limit = 3
	}

	collection = db.NormalizeDocsTableName(collection)
	cacheTable := db.GetCacheTableName(collection)

	// 1. Generate Query Vector Embedding using Gemini 2
	queryVec, err := c.embedder.GenerateQueryEmbedding(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("gagal generate query embedding (gemini 2): %w", err)
	}

	// 2. Check Semantic Cache (threshold kemiripan >= 0.92)
	const cacheSimilarityThreshold = 0.92
	cachedHit, err := db.FindCachedFeedbackIn(ctx, c.pool, cacheTable, queryVec, cacheSimilarityThreshold)
	if err != nil {
		log.Printf("Peringatan: gagal mencari query cache di %s: %v", cacheTable, err)
	} else if cachedHit != nil {
		_ = db.IncrementCacheHitIn(ctx, c.pool, cacheTable, cachedHit.ID)
		log.Printf("[CACHE HIT in %s] Query: '%s' cocok dengan riwayat: '%s' (kemiripan: %.2f%%)",
			cacheTable, queryText, cachedHit.QueryText, cachedHit.Similarity*100)

		var fb embedding.FeedbackResult
		if err := json.Unmarshal(cachedHit.Feedback, &fb); err != nil {
			log.Printf("Gagal parse cached feedback: %v", err)
		}

		var sources []MatchedSource
		if len(cachedHit.MatchedSources) > 0 {
			_ = json.Unmarshal(cachedHit.MatchedSources, &sources)
		}

		return &SearchExecutionResult{
			Status:          "success",
			Collection:      collection,
			Model:           c.embedder.GetModelName(),
			Source:          "cache",
			CacheHit:        true,
			MatchedHistory:  cachedHit.QueryText,
			CacheSimilarity: fmt.Sprintf("%.2f%%", cachedHit.Similarity*100),
			Query:           queryText,
			Feedback:        &fb,
			MatchedSources:  sources,
		}, nil
	}

	// 3. Cache Miss: Top K cosine similarity vector search in PostgreSQL
	results, err := db.SearchSimilarIn(ctx, c.pool, collection, queryVec, limit)
	if err != nil {
		return nil, fmt.Errorf("gagal melakukan pencarian vektor di database tabel '%s': %w", collection, err)
	}

	// 4. Convert retrieved documents to AI reference context
	var refContexts []embedding.ReferenceContext
	var matchedSources []MatchedSource
	for _, res := range results {
		refContexts = append(refContexts, embedding.ReferenceContext{
			ID:         res.ID,
			Category:   res.Category,
			Title:      res.Title,
			Content:    res.Content,
			Similarity: res.Similarity,
		})
		matchedSources = append(matchedSources, MatchedSource{
			ID:            res.ID,
			Category:      res.Category,
			Title:         res.Title,
			Similarity:    res.Similarity,
			SimilarityPct: fmt.Sprintf("%.2f%%", res.Similarity*100),
		})
	}

	// 5. Generate intelligent AI Feedback
	aiFeedback, err := c.embedder.GenerateFeedback(ctx, queryText, refContexts)
	if err != nil {
		return nil, fmt.Errorf("gagal memproses feedback AI: %w", err)
	}

	// 6. Save to semantic cache for instant future lookup
	if err := db.InsertQueryCacheIn(ctx, c.pool, cacheTable, queryText, queryVec, aiFeedback, matchedSources); err != nil {
		log.Printf("Peringatan: gagal menyimpan ke query cache %s: %v", cacheTable, err)
	}

	return &SearchExecutionResult{
		Status:         "success",
		Collection:     collection,
		Model:          c.embedder.GetModelName(),
		Source:         "generated",
		CacheHit:       false,
		Query:          queryText,
		Feedback:       aiFeedback,
		MatchedSources: matchedSources,
	}, nil
}

// HandleSemanticSearch handles POST /documents/search and GET /documents/search
func (c *DocumentController) HandleSemanticSearch(w http.ResponseWriter, r *http.Request) {
	var queryText string
	var requestedColl string
	limit := 3

	if r.Method == http.MethodGet {
		queryText = r.URL.Query().Get("q")
		if queryText == "" {
			queryText = r.URL.Query().Get("query")
		}
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
			limit = l
		}
		requestedColl = r.URL.Query().Get("collection")
	} else {
		var req SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		queryText = req.Query
		if req.Limit > 0 {
			limit = req.Limit
		}
		requestedColl = req.Collection
	}

	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		WriteError(w, http.StatusBadRequest, "Query pertanyaan atau keluhan tidak boleh kosong")
		return
	}

	collection := c.ResolveCollection(r, requestedColl)
	res, err := c.ExecuteSearch(r.Context(), collection, queryText, limit)
	if err != nil {
		log.Printf("Error execute search: %v", err)
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, res)
}
