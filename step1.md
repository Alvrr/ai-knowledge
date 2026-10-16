# Panduan Integrasi Fullstack: AI Knowledge Service (`apps/ai-knowledge`)

Dokumen ini adalah panduan lengkap (*cheat-sheet*) untuk developer Fullstack / Backend (`apps/api`, WhatsApp webhook, bot service, atau frontend) dalam menghubungkan dan mengonsumsi API dari microservice **AI Knowledge**.

---

## 1. Ringkasan Layanan & Arsitektur

Service **`ai-knowledge`** adalah microservice berbasis **Golang** yang bertindak sebagai mesin pencari semantik (*vector search engine*) dan basis pengetahuan internal (*RAG - Retrieval-Augmented Generation*).

- **Default Port**: `http://localhost:8080` (dapat disesuaikan di `.env`)
- **Tabel Basis Data**: `"docs_deploy"` (768 dimensi MRL)
- **Model Embedding**: Google Gemini Embedding 2 (`gemini-embedding-2` - 768 dimensi)
- **Kecepatan & Efisiensi**: Dilengkapi **Smart Caching Layer** (`query_deploy`). Pertanyaan serupa akan dijawab langsung dari cache (< 10 ms).
- **Anti-Halusinasi (Guardrail)**: Jika kemiripan (*similarity score*) di bawah `0.45` (45%), sistem otomatis mengembalikan pesan bahwa informasi belum tersedia di SOP resmi.

---

## 2. Cara Menjalankan Service

### Langkah Menjalankan Secara Lokal:
```bash
# 1. Pindah ke direktori service
cd apps/ai-knowledge

# 2. Pastikan file .env sudah terkonfigurasi dengan benar:
# PORT=8080
# DATABASE_URL="postgresql://user:password@localhost:5432/ai_knowledge?sslmode=disable"
# COLLECTION_NAME=docs_deploy
# GEMINI_API_KEY=your_gemini_api_key
# GEMINI_2_MODEL=gemini-embedding-2
# INTERNAL_API_KEY=ai_secret_internal_key_2026
# ADMIN_SECRET_KEY=admin_secret_super_2026

# 3. Jalankan seeding (jika database baru atau ingin reset data dummy)
go run cmd/seed/main.go

# 4. Jalankan service
go run main.go
```
Jika berhasil, terminal akan menampilkan:
```text
 [AI KNOWLEDGE SERVICE] REST API berjalan di http://localhost:8080
 🔒 Proteksi API Aktif: Request internal wajib menyertakan 'X-Internal-API-Key'
```

---

## 3. Autentikasi & Keamanan (API Key)

Kecuali endpoint `GET /` (health check), semua endpoint mewajibkan salah satu dari header berikut:

```http
X-Internal-API-Key: ai_secret_internal_key_2026
```
Atau:
```http
Authorization: Bearer ai_secret_internal_key_2026
```

Jika tidak disertakan atau salah, server akan merespon:
```json
{
  "status": "error",
  "message": "Akses ditolak: X-Internal-API-Key tidak valid atau tidak disertakan"
}
```

---

## 4. Endpoint Utama untuk WhatsApp Bot / Backend

### A. Semantic Search & AI Feedback (RAG)
Endpoint ini digunakan saat user menanyakan suatu hal atau mengirim keluhan melalui WhatsApp.

- **URL**: `POST /documents/search`
- **Headers**:
  - `Content-Type: application/json`
  - `X-Internal-API-Key: ai_secret_internal_key_2026`
- **Request Body**:
```json
{
  "query": "Saya mau perpanjang SIM tapi KTP saya luar kota, apakah bisa?",
  "limit": 3
}
```

- **Respon Sukses (Contoh Cache Hit / Generated)**:
```json
{
  "status": "success",
  "collection": "docs_deploy",
  "model": "gemini-embedding-2",
  "source": "generated",
  "cache_hit": false,
  "query": "Saya mau perpanjang SIM tapi KTP saya luar kota, apakah bisa?",
  "feedback": {
    "related_service": "Perpanjangan SIM Online / Luar Kota",
    "category": "Lalu Lintas",
    "short_description": "Perpanjangan SIM dengan KTP luar daerah dapat dilakukan di Satpas terdekat atau layanan SIM Keliling di seluruh Indonesia.",
    "long_summary": "Pemohon cukup membawa SIM lama, e-KTP, surat keterangan sehat, dan tes psikologi. Layanan dapat diakses langsung di gerai Satpas terdekat secara nasional tanpa harus pulang ke kota asal.",
    "best_similarity": 0.892,
    "best_similarity_percentage": "89.20%"
  },
  "matched_sources": [
    {
      "id": 45,
      "category": "Lalu Lintas",
      "title": "Perpanjangan SIM Online / Luar Kota",
      "similarity": 0.892,
      "similarity_percentage": "89.20%"
    }
  ]
}
```

---

## 5. CRUD Knowledge Management (Untuk Web Admin / Dashboard)

### A. Tambah Dokumen Pengetahuan Baru
- **Method**: `POST /documents`
- **Body**:
```json
{
  "category": "Layanan Publik",
  "title": "Pendaftaran Nikah di KUA",
  "content": "Syarat: Surat pengantar RT/RW, fotokopi KTP, KK, akta lahir, dan pas foto 2x3 serta 4x6 latar biru. Pendaftaran minimal 10 hari kerja sebelum akad."
}
```

### B. Ambil Daftar Dokumen
- **Method**: `GET /documents?limit=20&offset=0`

### C. Ambil Dokumen Berdasarkan ID
- **Method**: `GET /documents/{id}`

### D. Perbarui Dokumen
- **Method**: `PUT /documents/{id}`
- **Body**:
```json
{
  "category": "Layanan Publik",
  "title": "Pendaftaran Nikah di KUA (Revisi)",
  "content": "Pendaftaran kini wajib melalui sistem informasi SIMKAH Online."
}
```

### E. Hapus Dokumen
- **Method**: `DELETE /documents/{id}`

---

## 6. Ringkasan Error Handling

| Kode HTTP | Penjelasan | Cara Mengatasi |
|---|---|---|
| `401 Unauthorized` | API key salah atau tidak disertakan | Tambahkan header `X-Internal-API-Key` |
| `400 Bad Request` | Body JSON tidak valid atau query kosong | Periksa JSON payload |
| `404 Not Found` | ID dokumen tidak ditemukan | Periksa parameter ID |
| `500 Internal Error` | Gangguan database atau kuota Gemini | Periksa koneksi PostgreSQL atau kuota Google AI Studio |
