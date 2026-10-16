# AI Knowledge Service (pgvector + Google Gemini Embedding 2)

Microservice berbasis **Golang** untuk basis pengetahuan internal (*Knowledge Base*), pencarian semantik vektor (*Semantic Vector Search*), dan *Retrieval-Augmented Generation* (RAG) AI Feedback cerdas yang secara eksklusif ditenagai oleh **Google Gemini Embedding 2** (`gemini-embedding-2`) dan **PostgreSQL pgvector**.

---

## Fitur Utama

- **Google Gemini Embedding 2 (`gemini-embedding-2`)**:
  - Model embedding multimodal generasi terbaru dari Google dengan dukungan Matryoshka Representation Learning (MRL) skala 768 dimensi yang dioptimasi untuk akurasi dan kecepatan.
  - Task-specific asymmetric embedding: `RETRIEVAL_DOCUMENT` untuk dokumen tersimpan dan `RETRIEVAL_QUERY` untuk query pencarian.
- **PostgreSQL + pgvector (HNSW Indexing)**:
  - Pencarian kemiripan kosinus (*Cosine Similarity*) berkecepatan tinggi dengan indeks HNSW.
  - Otomatis fallback ke native SQL array & cosine function jika server PostgreSQL belum memasang ekstensi pgvector.
- **Smart Semantic Caching Layer**:
  - Menyimpan query embedding & respon AI di tabel `query_deploy`.
  - Pertanyaan serupa (similarity $\ge$ 92%) dijawab seketika dari cache (< 10 ms) tanpa memanggil Gemini LLM berulang kali.
- **AI Feedback Assistant**:
  - Merumuskan rekomendasi layanan/produk (`related_service`), inisiatif respon cepat (`short_description`), dan rangkuman prosedur/solusi lengkap (`long_summary`) dalam format JSON terstruktur.
- **Anti-Halusinasi (Guardrail)**:
  - Jika tingkat kemiripan dokumen terbaik di bawah `0.45` (45%), sistem otomatis memberikan respon aman bahwa dokumen resmi belum tersedia di database internal.
- **RESTful CRUD & Seeding Lengkap**:
  - Endpoint standar untuk tambah dokumen, baca (pagination & by ID), update (otomatis regenerate vector), hapus dokumen, reset bersih, dan auto-seeding.

---

## Persyaratan Sistem

- **Go**: Versi 1.22 atau lebih baru
- **PostgreSQL**: PostgreSQL 14+ dengan ekstensi `vector` (opsional namun sangat disarankan)
- **Google AI Studio API Key**: Untuk akses model `gemini-embedding-2`

---

## Konfigurasi Environment (`.env`)

Salin `.env.example` ke `.env`:

```env
PORT=8080
DATABASE_URL="postgresql://user:password@localhost:5432/ai_knowledge?sslmode=disable"

# Google Gemini Embedding 2 Configuration
GEMINI_API_KEY=your_gemini_api_key_here
GEMINI_2_MODEL=gemini-embedding-2
GEMINI_FEEDBACK_MODEL=gemini-2.5-flash

# Knowledge Base Collection Table
COLLECTION_NAME=docs_deploy

# Security Keys
INTERNAL_API_KEY=ai_secret_internal_key_2026
ADMIN_SECRET_KEY=admin_secret_super_2026
```

---

## Menjalankan Aplikasi

### 1. Inisialisasi & Seeding Data Dummy
Untuk memasukkan data SOP / layanan awal ke dalam PostgreSQL tabel `docs_deploy`:

```bash
go run cmd/seed/main.go
```

### 2. Menjalankan Server API
```bash
go run main.go
```

Output terminal saat server berjalan:
```text
 Mengkoneksikan ke PostgreSQL...
 Mode Aktif: pgvector Extension (HNSW Indexing)
 Berhasil terkoneksi ke PostgreSQL!
 Memverifikasi skema tabel database ('docs_deploy', 'query_deploy')...
 Skema database & pgvector siap digunakan.

 Inisialisasi Model Embedding:
   Model: Google Gemini Embedding 2 ('gemini-embedding-2' - 768 dimensi)
   Feedback AI: Google Gemini LLM Cerdas

 [AI KNOWLEDGE SERVICE] REST API berjalan di http://localhost:8080
 Koleksi Utama: 'docs_deploy' (Model: gemini-embedding-2)
```

---

## Dokumentasi API Endpoint

Semua endpoint internal (selain `GET /`) diproteksi dengan header:
- `X-Internal-API-Key: <INTERNAL_API_KEY>` atau `Authorization: Bearer <INTERNAL_API_KEY>`

### 1. Root & Health Check
- **Method / URL**: `GET http://localhost:8080/`
- **Response**: Info status service, model aktif, dan daftar endpoint.

### 2. Pencarian Semantik & AI Feedback
- **Method / URL**: `POST http://localhost:8080/documents/search`
- **Request Body**:
```json
{
  "query": "Bagaimana cara membuat atau mengurus KTP baru yang hilang?",
  "limit": 3
}
```
- **Response**:
```json
{
  "status": "success",
  "collection": "docs_deploy",
  "model": "gemini-embedding-2",
  "source": "generated",
  "cache_hit": false,
  "query": "Bagaimana cara membuat atau mengurus KTP baru yang hilang?",
  "feedback": {
    "related_service": "Penggantian KTP Rusak/Hilang",
    "category": "Kependudukan",
    "short_description": "Untuk mengurus KTP yang hilang, Anda dapat mengajukan permohonan penggantian KTP di Disdukcapil atau kantor kecamatan terdekat.",
    "long_summary": "Langkah pertama adalah membuat surat kehilangan di kantor polisi setempat, lalu siapkan fotokopi Kartu Keluarga (KK). Datangi kantor Disdukcapil atau kantor kecamatan untuk pencetakan ulang tanpa dipungut biaya.",
    "best_similarity": 0.8842,
    "best_similarity_percentage": "88.42%"
  },
  "matched_sources": [
    {
      "id": 12,
      "category": "Kependudukan",
      "title": "Penggantian KTP Rusak/Hilang",
      "similarity": 0.8842,
      "similarity_percentage": "88.42%"
    }
  ]
}
```

### 3. Tambah Dokumen Baru (Create)
- **Method / URL**: `POST http://localhost:8080/documents`
- **Request Body**:
```json
{
  "category": "Layanan Publik",
  "title": "Pembuatan Paspor Baru",
  "content": "Syarat: e-KTP, KK, Akta Kelahiran/Ijazah. Pendaftaran dilakukan melalui aplikasi M-Paspor."
}
```

### 4. Ambil Semua Dokumen (Read All)
- **Method / URL**: `GET http://localhost:8080/documents?limit=20&offset=0`

### 5. Ambil Dokumen Berdasarkan ID
- **Method / URL**: `GET http://localhost:8080/documents/{id}`

### 6. Perbarui Dokumen (Update)
- **Method / URL**: `PUT http://localhost:8080/documents/{id}`
- Mengupdate teks dokumen dan otomatis meregenerasi vektor embedding 768 dimensi menggunakan Gemini 2.

### 7. Hapus Dokumen (Delete)
- **Method / URL**: `DELETE http://localhost:8080/documents/{id}`

### 8. Reset Bersih Database & Cache
- **Method / URL**: `POST http://localhost:8080/documents/reset`
- **Header Khusus**: `X-Admin-Secret: <ADMIN_SECRET_KEY>`
- Mengosongkan tabel dokumen dan semantic query cache serta mereset urutan ID kembali ke 1.

### 9. Seed Otomatis Data Dummy
- **Method / URL**: `POST http://localhost:8080/documents/seed`
- Membaca file `dummy_datasedang.json` dan otomatis meng-generate vektor Gemini 2 lalu menyimpannya ke PostgreSQL.

---

## Struktur Folder Project

```text
ai-knowledge/
├── cmd/
│   └── seed/                     # CLI Seeder standar untuk Gemini 2 (tabel docs_deploy)
│       └── main.go
├── controller/                   # Handler REST API HTTP
│   ├── create.go                 # Handler tambah dokumen
│   ├── delete.go                 # Handler hapus dokumen
│   ├── document_controller.go    # Struct controller & route registration
│   ├── read.go                   # Handler baca dokumen (semua & by ID)
│   ├── response.go               # JSON response helpers
│   ├── search.go                 # Semantic search & smart caching logic
│   ├── seed.go                   # Handler reset & seed HTTP
│   └── update.go                 # Handler update dokumen
├── db/
│   └── db.go                     # Koneksi pgxpool, migrasi skema pgvector, query helpers
├── embedding/
│   ├── embedder.go               # Interface Embedder & struktur AI feedback
│   └── gemini2.go                # Klien Google Gemini Embedding 2 & AI Feedback
├── dummy_data.json               # Data dummy referensi
├── dummy_datasedang.json         # Data dummy referensi (100 SOP terstandarisasi)
├── dummy_datapanjang.json        # Data dummy komprehensif
├── .env                          # Konfigurasi environment aktif
├── .env.example                  # Template konfigurasi environment
├── go.mod                        # Modul Go & dependensi
├── go.sum                        # Checksum dependensi
├── main.go                       # Entrypoint aplikasi server
└── README.md                     # Dokumentasi project
```