package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go-pgvector-gemini/db"

	"github.com/pgvector/pgvector-go"
)

// HandleResetDatabase mengosongkan dan merombak tabel dokumen
func (c *DocumentController) HandleResetDatabase(w http.ResponseWriter, r *http.Request) {
	_, collection := c.ResolveEmbedderAndCollection(r, "")

	adminSecret := os.Getenv("ADMIN_SECRET_KEY")
	if adminSecret != "" {
		reqSecret := r.Header.Get("X-Admin-Secret")
		if reqSecret == "" {
			reqSecret = r.Header.Get("X-API-Key")
		}
		if reqSecret != adminSecret {
			WriteError(w, http.StatusForbidden, "Akses ditolak: Dibutuhkan header X-Admin-Secret yang valid untuk reset database")
			return
		}
	} else if os.Getenv("ENV") == "production" || os.Getenv("NODE_ENV") == "production" {
		WriteError(w, http.StatusForbidden, "Endpoint reset dinonaktifkan di environment production tanpa ADMIN_SECRET_KEY")
		return
	}

	if err := db.ResetTable(r.Context(), c.pool, collection); err != nil {
		log.Printf("Gagal reset database tabel '%s': %v", collection, err)
		WriteError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal merombak database tabel '%s': %v", collection, err))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"collection": collection,
		"message":    fmt.Sprintf("Tabel '%s' dan cache-nya berhasil dirombak bersih. Semua data lama telah dihapus dan ID direset ke 1.", collection),
	})
}

// HandleSeedDatabase memasukkan data dummy sekaligus
func (c *DocumentController) HandleSeedDatabase(w http.ResponseWriter, r *http.Request) {
	emb, collection := c.ResolveEmbedderAndCollection(r, "")
	if emb == nil {
		WriteError(w, http.StatusInternalServerError, "Tidak ada embedding model yang tersedia untuk koleksi ini")
		return
	}

	dataBytes, err := os.ReadFile("dummy_datasedang.json")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal membaca file data dummy: %v", err))
		return
	}
	sourceFile := "dummy_datasedang.json"

	var items []DocumentRequest
	if err := json.Unmarshal(dataBytes, &items); err != nil {
		var wrapper struct {
			Data []DocumentRequest `json:"data"`
		}
		if err2 := json.Unmarshal(dataBytes, &wrapper); err2 != nil {
			WriteError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal parse %s: %v", sourceFile, err))
			return
		}
		items = wrapper.Data
	}

	type InsertedItem struct {
		ID       int    `json:"id"`
		Category string `json:"category"`
		Title    string `json:"title"`
	}

	log.Printf("Memulai proses seeding %d data ke tabel '%s' dari %s (Model: %s)...", len(items), collection, sourceFile, emb.GetModelName())
	var inserted []InsertedItem
	for i, item := range items {
		titleWithCat := fmt.Sprintf("[%s] %s", item.Category, item.Title)

		var vec pgvector.Vector
		var err error

		for attempt := 1; attempt <= 4; attempt++ {
			vec, err = emb.GenerateDocumentEmbedding(r.Context(), titleWithCat, item.Content)
			if err == nil {
				break
			}
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				log.Printf("[%d/%d] Rate limit hit. Menunggu 30 detik...", i+1, len(items))
				time.Sleep(30 * time.Second)
				continue
			}
			time.Sleep(1 * time.Second)
		}

		if err != nil {
			log.Printf("Gagal generate embedding pada data ke-%d ('%s'): %v", i+1, item.Title, err)
			WriteError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal generate embedding '%s': %v", item.Title, err))
			return
		}

		id, err := db.InsertDocumentTo(r.Context(), c.pool, collection, item.Category, item.Title, item.Content, vec)
		if err != nil {
			log.Printf("Gagal menyimpan ke database pada data ke-%d ('%s'): %v", i+1, item.Title, err)
			WriteError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal menyimpan '%s': %v", item.Title, err))
			return
		}

		inserted = append(inserted, InsertedItem{
			ID:       id,
			Category: item.Category,
			Title:    item.Title,
		})

		if (i+1)%10 == 0 || i+1 == len(items) {
			log.Printf("Progress seeding: %d/%d data berhasil diproses.", i+1, len(items))
		}

		time.Sleep(200 * time.Millisecond)
	}

	WriteJSON(w, http.StatusCreated, map[string]any{
		"status":         "success",
		"collection":     collection,
		"model":          emb.GetModelName(),
		"message":        fmt.Sprintf("Berhasil memasukkan %d data dummy dengan vector embedding otomatis ke '%s'", len(inserted), collection),
		"total_inserted": len(inserted),
		"data":           inserted,
	})
}
