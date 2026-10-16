package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"google.golang.org/genai"
)

// Gemini2Embedder represents Google's Gemini Embedding 2 model (with Matryoshka Representation Learning dimension scaling).
type Gemini2Embedder struct {
	client    *genai.Client
	model     string
	dimension int32
}

// NewGemini2Embedder creates a new embedder specifically for Gemini Embedding 2 (default model: 'gemini-embedding-2').
func NewGemini2Embedder(ctx context.Context, apiKey, modelName string, dimension int) (*Gemini2Embedder, error) {
	if modelName == "" {
		modelName = "gemini-embedding-2"
	}
	if dimension <= 0 {
		dimension = 768 // Default MRL dimension for balanced accuracy, speed, and PostgreSQL vector compatibility
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client for gemini 2: %w", err)
	}

	return &Gemini2Embedder{
		client:    client,
		model:     modelName,
		dimension: int32(dimension),
	}, nil
}

// Close is a no-op placeholder for clean interface lifecycle.
func (e *Gemini2Embedder) Close() {
	// genai.Client uses standard HTTP transport
}

// GenerateDocumentEmbedding creates an asymmetric vector embedding specifically optimized
// for storing informational documents (RETRIEVAL_DOCUMENT) with explicit title weighting.
func (e *Gemini2Embedder) GenerateDocumentEmbedding(ctx context.Context, title, content string) (pgvector.Vector, error) {
	dim := e.dimension
	res, err := e.client.Models.EmbedContent(ctx, e.model, []*genai.Content{
		{
			Parts: []*genai.Part{
				{Text: content},
			},
		},
	}, &genai.EmbedContentConfig{
		OutputDimensionality: &dim,
		TaskType:             "RETRIEVAL_DOCUMENT",
		Title:                title,
	})
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("failed to generate document embedding (gemini 2): %w", err)
	}

	if len(res.Embeddings) == 0 || len(res.Embeddings[0].Values) == 0 {
		return pgvector.Vector{}, fmt.Errorf("received empty document embeddings from Gemini Embedding 2")
	}

	return pgvector.NewVector(res.Embeddings[0].Values), nil
}

// GenerateQueryEmbedding creates an asymmetric vector embedding specifically optimized
// for search queries (RETRIEVAL_QUERY) to accurately retrieve relevant documents.
func (e *Gemini2Embedder) GenerateQueryEmbedding(ctx context.Context, query string) (pgvector.Vector, error) {
	dim := e.dimension
	res, err := e.client.Models.EmbedContent(ctx, e.model, []*genai.Content{
		{
			Parts: []*genai.Part{
				{Text: query},
			},
		},
	}, &genai.EmbedContentConfig{
		OutputDimensionality: &dim,
		TaskType:             "RETRIEVAL_QUERY",
	})
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("failed to generate query embedding (gemini 2): %w", err)
	}

	if len(res.Embeddings) == 0 || len(res.Embeddings[0].Values) == 0 {
		return pgvector.Vector{}, fmt.Errorf("received empty query embeddings from Gemini Embedding 2")
	}

	return pgvector.NewVector(res.Embeddings[0].Values), nil
}

// GenerateEmbedding provides a fallback embedding without specific task type.
func (e *Gemini2Embedder) GenerateEmbedding(ctx context.Context, text string) (pgvector.Vector, error) {
	return e.GenerateQueryEmbedding(ctx, text)
}

// GetDimension returns the active vector dimension (e.g. 768).
func (e *Gemini2Embedder) GetDimension() int {
	return int(e.dimension)
}

// GetModelName returns the model identifier string.
func (e *Gemini2Embedder) GetModelName() string {
	return e.model
}

// GetClient returns the underlying Google GenAI client.
func (e *Gemini2Embedder) GetClient() *genai.Client {
	return e.client
}

// GenerateFeedback uses Gemini LLM to generate an actionable AI feedback response based on vector search results.
func (e *Gemini2Embedder) GenerateFeedback(ctx context.Context, userQuery string, refs []ReferenceContext) (*FeedbackResult, error) {
	return GenerateGeminiFeedback(ctx, e.client, userQuery, refs)
}

// GenerateGeminiFeedback generates intelligent AI feedback using Gemini LLM.
func GenerateGeminiFeedback(ctx context.Context, client *genai.Client, userQuery string, refs []ReferenceContext) (*FeedbackResult, error) {
	if client == nil {
		return nil, fmt.Errorf("gemini client is nil")
	}

	if len(refs) == 0 {
		return &FeedbackResult{
			RelatedService:    "Tidak Ditemukan",
			Category:          "Umum",
			ShortDescription:  "Mohon maaf, kami belum menemukan layanan atau informasi yang sesuai dengan pertanyaan atau keluhan Anda.",
			LongSummary:       "Silakan periksa kembali kata kunci pertanyaan atau hubungi petugas layanan pelanggan untuk bantuan lebih lanjut.",
			BestSimilarity:    0,
			BestSimilarityPct: "0.00%",
		}, nil
	}

	best := refs[0]

	// Guardrail: jika similarity terbaik terlalu rendah (< 0.45), jangan paksakan LLM berhalusinasi
	const minSimilarityThreshold = 0.45
	if best.Similarity < minSimilarityThreshold {
		return &FeedbackResult{
			RelatedService:    "Tidak Ditemukan",
			Category:          "Umum",
			ShortDescription:  "Mohon maaf, pertanyaan Anda belum ditemukan kecocokan dokumen resminya di pangkalan data internal.",
			LongSummary:       "Silakan ajukan pertanyaan yang lebih spesifik atau hubungi petugas layanan pelanggan untuk bantuan lebih lanjut.",
			BestSimilarity:    best.Similarity,
			BestSimilarityPct: fmt.Sprintf("%.2f%%", best.Similarity*100),
		}, nil
	}

	// Format konteks referensi dokumen
	var contextBuilder strings.Builder
	for i, ref := range refs {
		if i >= 3 {
			break // Ambil maksimal 3 dokumen teratas sebagai konteks
		}
		contextBuilder.WriteString(fmt.Sprintf("\n--- Dokumen #%d ---\nKategori: %s\nJudul: %s\nIsi: %s\nTingkat Kemiripan: %.2f%%\n",
			i+1, ref.Category, ref.Title, ref.Content, ref.Similarity*100))
	}

	prompt := fmt.Sprintf(`Kamu adalah asisten AI customer service dan layanan publik cerdas yang responsif, solutif, dan berempati tinggi.
Tugas utamamu:
Berikan umpan balik / tanggapan membalas pertanyaan atau keluhan pengguna berdasarkan dokumen referensi SOP/layanan yang paling mendekati dan relevan di bawah ini.

Pertanyaan / Keluhan Pengguna:
"%s"

Dokumen Referensi Terkait (Hasil Pencarian Kemiripan Vektor):
%s

Instruksi:
1. "related_service": Sebutkan nama layanan atau produk terkait secara spesifik berdasarkan dokumen referensi terbaik.
2. "category": Kategori dari layanan tersebut.
3. "short_description": Inisiatif respon cepat AI (1-2 kalimat) yang ramah dan solutif, langsung menyebutkan nama produk/layanan terkait untuk menjawab pertanyaan atau keluhan pengguna.
4. "long_summary": Deskripsi panjang yang merangkum solusi lengkap, alur/prosedur yang harus dijalani, syarat berkas yang diperlukan, serta tempat atau pihak yang harus dihubungi berdasarkan dokumen referensi.

Kembalikan HANYA format JSON valid tanpa tanda kutip pembuka/penutup markdown seperti `+"```"+`json:
{
  "related_service": "...",
  "category": "...",
  "short_description": "...",
  "long_summary": "..."
}`, userQuery, contextBuilder.String())

	feedbackModel := os.Getenv("GEMINI_FEEDBACK_MODEL")
	modelsToTry := []string{}
	if feedbackModel != "" {
		modelsToTry = append(modelsToTry, feedbackModel)
	}
	modelsToTry = append(modelsToTry, "gemini-2.5-flash", "gemini-flash-lite-latest", "gemini-3.5-flash-lite")

	var resp *genai.GenerateContentResponse
	var err error

	genConfig := &genai.GenerateContentConfig{
		MaxOutputTokens:  500,
		ResponseMIMEType: "application/json",
	}

	for _, model := range modelsToTry {
		for attempt := 1; attempt <= 2; attempt++ {
			resp, err = client.Models.GenerateContent(ctx, model, []*genai.Content{
				{
					Parts: []*genai.Part{
						{Text: prompt},
					},
				},
			}, genConfig)
			if err == nil && len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if err == nil && len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
			break
		}
	}

	if err != nil || len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		// Fallback jika pemanggilan LLM gagal
		return &FeedbackResult{
			RelatedService:    best.Title,
			Category:          best.Category,
			ShortDescription:  fmt.Sprintf("Terkait pertanyaan atau keluhan Anda, informasi yang relevan adalah '%s'.", best.Title),
			LongSummary:       best.Content,
			BestSimilarity:    best.Similarity,
			BestSimilarityPct: fmt.Sprintf("%.2f%%", best.Similarity*100),
		}, nil
	}

	rawText := resp.Candidates[0].Content.Parts[0].Text
	cleanJSON := strings.TrimSpace(rawText)
	cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
	cleanJSON = strings.TrimPrefix(cleanJSON, "```")
	cleanJSON = strings.TrimSuffix(cleanJSON, "```")
	cleanJSON = strings.TrimSpace(cleanJSON)

	var result FeedbackResult
	if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
		result.RelatedService = best.Title
		result.Category = best.Category
		result.ShortDescription = fmt.Sprintf("Berdasarkan pertanyaan Anda, layanan yang paling tepat adalah '%s'.", best.Title)
		result.LongSummary = rawText
	}

	result.BestSimilarity = best.Similarity
	result.BestSimilarityPct = fmt.Sprintf("%.2f%%", best.Similarity*100)

	return &result, nil
}
