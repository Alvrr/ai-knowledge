package controller

import (
	"net/http"

	"go-pgvector-gemini/db"
	"go-pgvector-gemini/embedding"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DocumentController struct {
	pool       *pgxpool.Pool
	embedder   embedding.Embedder
	collection string
}

func NewDocumentController(pool *pgxpool.Pool, embedder embedding.Embedder, collection string) *DocumentController {
	if collection == "" {
		collection = db.GetDefaultTable()
	}
	return &DocumentController{
		pool:       pool,
		embedder:   embedder,
		collection: collection,
	}
}

// ResolveCollection returns the target collection name.
func (c *DocumentController) ResolveCollection(r *http.Request, explicitColl string) string {
	coll := explicitColl
	if coll == "" && r != nil {
		coll = r.URL.Query().Get("collection")
	}
	if coll == "" {
		coll = c.collection
	}
	return db.SanitizeTableName(coll)
}

// ResolveEmbedderAndCollection provides compatibility with handlers.
func (c *DocumentController) ResolveEmbedderAndCollection(r *http.Request, explicitColl string) (embedding.Embedder, string) {
	return c.embedder, c.ResolveCollection(r, explicitColl)
}

// DocumentRequest is the shared payload format for creating and updating documents.
type DocumentRequest struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

// RegisterRoutes registers all document-related routes to the ServeMux.
func (c *DocumentController) RegisterRoutes(mux *http.ServeMux) {
	// Root API Info & Health Check
	mux.HandleFunc("GET /", c.HandleRoot)

	// CRUD Documents
	mux.HandleFunc("GET /documents", c.HandleGetAllDocuments)
	mux.HandleFunc("GET /documents/{id}", c.HandleGetDocumentByID)
	mux.HandleFunc("POST /documents", c.HandleCreateDocument)
	mux.HandleFunc("PUT /documents/{id}", c.HandleUpdateDocument)
	mux.HandleFunc("DELETE /documents/{id}", c.HandleDeleteDocument)

	// Database Maintenance: Reset & Seed
	mux.HandleFunc("POST /documents/reset", c.HandleResetDatabase)
	mux.HandleFunc("POST /documents/seed", c.HandleSeedDatabase)

	// Semantic Vector Search & AI Feedback
	mux.HandleFunc("POST /documents/search", c.HandleSemanticSearch)
	mux.HandleFunc("GET /documents/search", c.HandleSemanticSearch)
}

func (c *DocumentController) HandleRoot(w http.ResponseWriter, r *http.Request) {
	totalDocs, _ := db.CountDocumentsIn(r.Context(), c.pool, c.collection)

	WriteJSON(w, http.StatusOK, map[string]any{
		"service":            "AI Knowledge Service (Gemini Embedding 2 + pgvector RAG)",
		"status":             "running",
		"model":              c.embedder.GetModelName(),
		"embedding_provider": "Google Gemini Embedding 2",
		"vector_dimension":   c.embedder.GetDimension(),
		"active_collection":  c.collection,
		"cache_table":        db.GetCacheTableName(c.collection),
		"total_documents":    totalDocs,
		"endpoints": map[string]string{
			"POST /documents":        "Tambah dokumen baru dan buat vector embedding otomatis",
			"GET /documents":         "Lihat daftar semua dokumen (?limit=50&offset=0)",
			"GET /documents/{id}":    "Lihat detail dokumen berdasarkan ID",
			"PUT /documents/{id}":    "Update judul/konten dokumen dan regenerate embedding",
			"DELETE /documents/{id}": "Hapus dokumen",
			"POST /documents/reset":  "Reset bersih tabel dokumen & query cache (membutuhkan X-Admin-Secret)",
			"POST /documents/seed":   "Seed data SOP/informasi dummy secara otomatis",
			"POST /documents/search": "Pencarian semantik cerdas + AI Feedback ({ \"query\": \"...\", \"limit\": 3 })",
		},
	})
}
