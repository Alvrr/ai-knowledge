package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go-pgvector-gemini/controller"
	"go-pgvector-gemini/db"
	"go-pgvector-gemini/embedding"

	"github.com/joho/godotenv"
)

func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Internal-API-Key, X-API-Key, X-Admin-Secret")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func authMiddleware(next http.Handler) http.Handler {
	internalKey := os.Getenv("INTERNAL_API_KEY")
	if internalKey == "" {
		internalKey = os.Getenv("AI_SERVICE_API_KEY")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Root "/" dipersilakan untuk health check tanpa API key
		if r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}

		if internalKey != "" {
			reqKey := r.Header.Get("X-Internal-API-Key")
			if reqKey == "" {
				reqKey = r.Header.Get("X-API-Key")
			}
			if reqKey == "" {
				authHeader := r.Header.Get("Authorization")
				if strings.HasPrefix(authHeader, "Bearer ") {
					reqKey = strings.TrimPrefix(authHeader, "Bearer ")
				}
			}

			if reqKey != internalKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"status":"error","message":"Akses ditolak: X-Internal-API-Key tidak valid atau tidak disertakan"}`))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Peringatan: file .env tidak ditemukan, menggunakan environment variable sistem.")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("Error: DATABASE_URL tidak diatur di file .env")
	}

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Fatal("Error: GEMINI_API_KEY wajib diatur di file .env")
	}

	gemini2Model := os.Getenv("GEMINI_2_MODEL")
	if gemini2Model == "" {
		gemini2Model = "gemini-embedding-2"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	collection := os.Getenv("COLLECTION_NAME")
	if collection == "" {
		collection = db.DefaultDocsTable
	} else {
		collection = db.SanitizeTableName(collection)
	}

	ctx := context.Background()

	fmt.Println(" Mengkoneksikan ke PostgreSQL...")
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("Gagal konek ke database: %v", err)
	}
	defer pool.Close()
	fmt.Println(" Berhasil terkoneksi ke PostgreSQL!")

	fmt.Printf(" Memverifikasi skema tabel database ('%s', '%s')...\n", collection, db.GetCacheTableName(collection))
	if err := db.InitSchema(ctx, pool); err != nil {
		log.Fatalf("Gagal inisialisasi schema: %v", err)
	}
	fmt.Println(" Skema database & pgvector siap digunakan.")

	fmt.Println("\n Inisialisasi Model Embedding:")
	embedder, err := embedding.NewGemini2Embedder(ctx, geminiKey, gemini2Model, db.VectorDimension)
	if err != nil {
		log.Fatalf("Gagal inisialisasi Gemini Embedding 2: %v", err)
	}
	defer embedder.Close()
	fmt.Printf("   Model: Google Gemini Embedding 2 ('%s' - %d dimensi)\n", gemini2Model, embedder.GetDimension())
	fmt.Println("   Feedback AI: Google Gemini LLM Cerdas")

	mux := http.NewServeMux()

	// Inisialisasi DocumentController
	docController := controller.NewDocumentController(pool, embedder, collection)
	docController.RegisterRoutes(mux)

	addr := ":" + port
	fmt.Printf("\n [AI KNOWLEDGE SERVICE] REST API berjalan di http://localhost%s\n", addr)
	fmt.Printf(" Koleksi Utama: '%s' (Model: %s)\n", collection, gemini2Model)
	fmt.Println("-----------------------------------------------------------------")
	fmt.Printf(" [GET]    http://localhost%s/                  -> Service Info & Health Check\n", addr)
	fmt.Printf(" [POST]   http://localhost%s/documents         -> Tambah Dokumen Baru\n", addr)
	fmt.Printf(" [GET]    http://localhost%s/documents         -> Lihat Semua Dokumen (?limit=50&offset=0)\n", addr)
	fmt.Printf(" [GET]    http://localhost%s/documents/{id}    -> Lihat Dokumen by ID\n", addr)
	fmt.Printf(" [PUT]    http://localhost%s/documents/{id}    -> Update Dokumen & Regenerate Vector\n", addr)
	fmt.Printf(" [DELETE] http://localhost%s/documents/{id}    -> Hapus Dokumen\n", addr)
	fmt.Printf(" [POST]   http://localhost%s/documents/reset   -> Reset Bersih Tabel & Cache (Admin)\n", addr)
	fmt.Printf(" [POST]   http://localhost%s/documents/seed    -> Seed Otomatis Data Dummy\n", addr)
	fmt.Printf(" [POST]   http://localhost%s/documents/search  -> Pencarian Semantik + AI Feedback\n", addr)
	fmt.Println("-----------------------------------------------------------------")

	if os.Getenv("INTERNAL_API_KEY") != "" || os.Getenv("AI_SERVICE_API_KEY") != "" {
		fmt.Println(" Proteksi API Aktif: Request internal wajib menyertakan 'X-Internal-API-Key'")
	} else {
		fmt.Println(" Perhatian: INTERNAL_API_KEY belum diatur di .env (Akses internal belum diproteksi)")
	}

	server := &http.Server{
		Addr:    addr,
		Handler: enableCORS(authMiddleware(mux)),
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server gagal berjalan: %v", err)
		}
	}()

	<-stopChan
	fmt.Println("\n Menutup server dengan graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Gagal graceful shutdown: %v", err)
	}
	fmt.Println(" Server dan koneksi database ditutup dengan aman. Sampai jumpa!")
}
