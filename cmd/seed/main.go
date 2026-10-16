package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go-pgvector-gemini/db"
	"go-pgvector-gemini/embedding"

	"github.com/joho/godotenv"
	"github.com/pgvector/pgvector-go"
)

type DocumentItem struct {
	ID        int       `json:"id"`
	Category  string    `json:"category"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Peringatan: file .env tidak ditemukan, menggunakan environment variable.")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL tidak diatur di .env")
	}

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Fatal("GEMINI_API_KEY tidak diatur di .env")
	}

	modelName := os.Getenv("GEMINI_2_MODEL")
	if modelName == "" {
		modelName = "gemini-embedding-2"
	}

	targetTable := os.Getenv("COLLECTION_NAME")
	if targetTable == "" {
		targetTable = db.DefaultDocsTable
	} else {
		targetTable = db.SanitizeTableName(targetTable)
	}

	cacheTable := db.GetCacheTableName(targetTable)
	ctx := context.Background()

	fmt.Println("==================================================================")
	fmt.Printf("🚀 MEMULAI SEEDING DATA GEMINI EMBEDDING 2 KE TABEL '%s'\n", targetTable)
	fmt.Printf("Model Embedding: %s (%d dimensi)\n", modelName, db.VectorDimension)
	fmt.Printf("Tabel Cache:     %s\n", cacheTable)
	fmt.Println("==================================================================")

	fmt.Println("\n1. Mengkoneksikan ke PostgreSQL...")
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("Gagal konek DB: %v", err)
	}
	defer pool.Close()
	fmt.Println("   Berhasil terkoneksi ke PostgreSQL!")

	fmt.Printf("\n2. Memastikan skema tabel '%s' & '%s'...\n", targetTable, cacheTable)
	if err := db.InitSchema(ctx, pool); err != nil {
		log.Fatalf("Gagal inisialisasi schema: %v", err)
	}

	fmt.Printf("\n3. Mengosongkan tabel '%s'...\n", targetTable)
	_, err = pool.Exec(ctx, fmt.Sprintf(`TRUNCATE TABLE "%s" RESTART IDENTITY CASCADE;`, targetTable))
	if err != nil {
		log.Fatalf("Gagal truncate tabel: %v", err)
	}
	_, _ = pool.Exec(ctx, fmt.Sprintf(`TRUNCATE TABLE "%s" RESTART IDENTITY CASCADE;`, cacheTable))
	fmt.Printf("   Tabel '%s' bersih dan siap diisi mulai ID 1.\n", targetTable)

	fmt.Println("\n4. Membaca file dummy_datasedang.json...")
	dataBytes, err := os.ReadFile("dummy_datasedang.json")
	if err != nil {
		log.Fatalf("Gagal membaca file data dummy: %v", err)
	}
	sourceFile := "dummy_datasedang.json"

	var items []DocumentItem
	if err := json.Unmarshal(dataBytes, &items); err != nil {
		var wrapper struct {
			Data []DocumentItem `json:"data"`
		}
		if err2 := json.Unmarshal(dataBytes, &wrapper); err2 != nil {
			log.Fatalf("Gagal parse %s: %v", sourceFile, err)
		}
		items = wrapper.Data
	}

	fmt.Printf("   Ditemukan %d item data dari %s untuk dimasukkan ke '%s'.\n", len(items), sourceFile, targetTable)

	fmt.Println("\n5. Inisialisasi Gemini Embedding 2 Client...")
	embedder, err := embedding.NewGemini2Embedder(ctx, geminiKey, modelName, db.VectorDimension)
	if err != nil {
		log.Fatalf("Gagal inisialisasi Gemini client: %v", err)
	}
	defer embedder.Close()

	fmt.Printf("\n6. Memproses embedding Gemini 2 dan menyimpan ke '%s'...\n", targetTable)
	successCount := 0
	startTime := time.Now()

	for i, item := range items {
		titleWithCat := fmt.Sprintf("[%s] %s", item.Category, item.Title)

		var vec pgvector.Vector
		var err error

		for attempt := 1; attempt <= 5; attempt++ {
			vec, err = embedder.GenerateDocumentEmbedding(ctx, titleWithCat, item.Content)
			if err == nil {
				break
			}

			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				log.Printf("[%d/%d] Rate limit hit. Menunggu 30 detik (attempt %d)...", i+1, len(items), attempt)
				time.Sleep(30 * time.Second)
				continue
			}

			log.Printf("[%d/%d] Error embed '%s': %v (attempt %d). Retry dalam 2 detik...", i+1, len(items), item.Title, err, attempt)
			time.Sleep(2 * time.Second)
		}

		if err != nil {
			log.Printf("[%d/%d] Gagal embed '%s' setelah retry: %v. Melewati item ini.", i+1, len(items), item.Title, err)
			continue
		}

		newID, insErr := db.InsertDocumentTo(ctx, pool, targetTable, item.Category, item.Title, item.Content, vec)
		if insErr != nil {
			log.Printf("[%d/%d] Gagal simpan ke DB '%s': %v", i+1, len(items), item.Title, insErr)
			continue
		}

		successCount++
		if successCount%10 == 0 || successCount == len(items) {
			fmt.Printf("   [Progress %d/%d] Berhasil disimpan (ID: %d) -> '%s'\n", successCount, len(items), newID, item.Title)
		}

		time.Sleep(200 * time.Millisecond)
	}

	elapsed := time.Since(startTime)
	fmt.Printf("\n🎉 SELESAI! Berhasil memasukkan %d dari %d dokumen ke tabel '%s' (Waktu: %v).\n", successCount, len(items), targetTable, elapsed.Round(time.Second))

	var totalDocs int
	_ = pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s";`, targetTable)).Scan(&totalDocs)
	fmt.Printf("📊 Verifikasi Database: Total baris pada '%s' = %d baris.\n", targetTable, totalDocs)
}
