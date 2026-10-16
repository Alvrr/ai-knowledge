package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

type QueryCacheHit struct {
	ID             int             `json:"id"`
	QueryText      string          `json:"query_text"`
	Feedback       json.RawMessage `json:"feedback"`
	MatchedSources json.RawMessage `json:"matched_sources"`
	Similarity     float64         `json:"similarity"`
	HitCount       int             `json:"hit_count"`
}

type Document struct {
	ID        int
	Category  string
	Title     string
	Content   string
	Embedding pgvector.Vector
	CreatedAt time.Time
}

type SearchResult struct {
	ID         int     `json:"id"`
	Category   string  `json:"category"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}

// DocumentResponse represents document data without raw vector for JSON responses.
type DocumentResponse struct {
	ID        int       `json:"id"`
	Category  string    `json:"category"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// HasPgVector indicates if the PostgreSQL server supports the pgvector extension.
var HasPgVector bool

var validTableRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

const (
	DefaultDocsTable  = "docs_deploy"
	DefaultCacheTable = "query_deploy"
	VectorDimension   = 768
)

// NormalizeDocsTableName returns the Gemini 2 table name.
func NormalizeDocsTableName(collection string) string {
	clean := strings.TrimSpace(collection)
	if clean == "" {
		if envCol := os.Getenv("COLLECTION_NAME"); envCol != "" {
			return envCol
		}
		return DefaultDocsTable
	}
	return clean
}

// SanitizeTableName ensures the table name is safe for SQL identifier use.
func SanitizeTableName(table string) string {
	clean := strings.TrimSpace(table)
	if !validTableRegex.MatchString(clean) {
		return DefaultDocsTable
	}
	return clean
}

// GetDefaultTable returns the default collection/table name from environment or fallback.
func GetDefaultTable() string {
	col := os.Getenv("COLLECTION_NAME")
	if col != "" {
		return SanitizeTableName(col)
	}
	return DefaultDocsTable
}

// GetCacheTableName returns the query cache table name for the given collection.
func GetCacheTableName(collection string) string {
	clean := SanitizeTableName(collection)
	if clean == "docs_deploy" || strings.Contains(clean, "deploy") {
		return "query_deploy"
	}
	if strings.Contains(clean, "cache") {
		return clean
	}
	// Standardized naming convention
	return DefaultCacheTable
}

// EnsureDatabaseAndExtension ensures database connection and vector search capabilities exist.
func EnsureDatabaseAndExtension(ctx context.Context, connStr string) error {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return fmt.Errorf("unable to parse db config: %w", err)
	}

	targetDB := config.ConnConfig.Database
	if targetDB != "" && targetDB != "postgres" {
		adminConfig := config.Copy()
		adminConfig.ConnConfig.Database = "postgres"
		adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
		if err == nil {
			var exists bool
			checkQuery := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1);"
			if err := adminPool.QueryRow(ctx, checkQuery, targetDB).Scan(&exists); err == nil && !exists {
				createQuery := fmt.Sprintf(`CREATE DATABASE "%s";`, targetDB)
				_, _ = adminPool.Exec(ctx, createQuery)
				fmt.Printf(" Database '%s' berhasil dibuat.\n", targetDB)
			}
			adminPool.Close()
		}
	}

	rawConn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed connecting to target db: %w", err)
	}
	defer rawConn.Close(ctx)

	// Check if vector extension is available in PostgreSQL
	if _, err := rawConn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector;"); err == nil {
		HasPgVector = true
		fmt.Println(" Mode Aktif: pgvector Extension (HNSW Indexing)")
	} else {
		HasPgVector = false
		fmt.Println(" Mode Aktif: Native PostgreSQL Float Array + Cosine Similarity Function")
		funcQuery := `
CREATE OR REPLACE FUNCTION cosine_similarity(a double precision[], b double precision[])
RETURNS double precision AS $$
DECLARE
    dot_product double precision := 0;
    norm_a double precision := 0;
    norm_b double precision := 0;
    i int;
    len int := array_length(a, 1);
BEGIN
    FOR i IN 1..len LOOP
        dot_product := dot_product + a[i] * b[i];
        norm_a := norm_a + a[i] * a[i];
        norm_b := norm_b + b[i] * b[i];
    END LOOP;
    IF norm_a = 0 OR norm_b = 0 THEN
        RETURN 0;
    END IF;
    RETURN dot_product / (sqrt(norm_a) * sqrt(norm_b));
END;
$$ LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE;`
		if _, err := rawConn.Exec(ctx, funcQuery); err != nil {
			return fmt.Errorf("failed creating fallback cosine_similarity function: %w", err)
		}
	}

	return nil
}

// Connect establishes a connection pool to PostgreSQL.
func Connect(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
	if err := EnsureDatabaseAndExtension(ctx, connStr); err != nil {
		return nil, fmt.Errorf("ensure database & extension error: %w", err)
	}

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database config: %w", err)
	}

	if HasPgVector {
		config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			return pgxvec.RegisterTypes(ctx, conn)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	return pool, nil
}

// InitSchema ensures the Gemini 2 knowledge table and query cache table exist.
func InitSchema(ctx context.Context, pool *pgxpool.Pool) error {
	defaultTbl := GetDefaultTable()
	cacheTbl := GetCacheTableName(defaultTbl)

	if HasPgVector {
		// 1. Pastikan tabel ada
		createTableDocs := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
			id SERIAL PRIMARY KEY,
			category VARCHAR(100) NOT NULL DEFAULT 'Umum',
			title VARCHAR(255) NOT NULL,
			content TEXT NOT NULL,
			embedding VECTOR(%d) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`, defaultTbl, VectorDimension)

		createTableCache := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
			id SERIAL PRIMARY KEY,
			query_text TEXT NOT NULL,
			query_embedding VECTOR(%d) NOT NULL,
			feedback JSONB NOT NULL,
			matched_sources JSONB DEFAULT '[]'::jsonb,
			hit_count INT DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`, cacheTbl, VectorDimension)

		if _, err := pool.Exec(ctx, createTableDocs); err != nil {
			return fmt.Errorf("failed creating table %s: %w", defaultTbl, err)
		}
		if _, err := pool.Exec(ctx, createTableCache); err != nil {
			return fmt.Errorf("failed creating cache table %s: %w", cacheTbl, err)
		}

		// 2. Jika kolom embedding sebelumnya dibuat dengan tipe double precision[] (_float8), migrasikan ke vector(768)
		migrateToVector := func(tableName, colName string) {
			var udt string
			checkQ := `SELECT udt_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2;`
			if err := pool.QueryRow(ctx, checkQ, tableName, colName).Scan(&udt); err == nil {
				if udt == "_float8" || strings.Contains(udt, "float") {
					alterQ := fmt.Sprintf(`ALTER TABLE "%s" ALTER COLUMN "%s" TYPE vector(%d) USING ('[' || array_to_string("%s", ',') || ']')::vector(%d);`,
						tableName, colName, VectorDimension, colName, VectorDimension)
					if _, err := pool.Exec(ctx, alterQ); err != nil {
						fmt.Printf(" Peringatan: gagal migrasi kolom %s.%s ke vector: %v\n", tableName, colName, err)
					} else {
						fmt.Printf(" Berhasil migrasi kolom '%s.%s' dari double precision[] ke VECTOR(%d).\n", tableName, colName, VectorDimension)
					}
				}
			}
		}

		migrateToVector(defaultTbl, "embedding")
		migrateToVector(cacheTbl, "query_embedding")

		// 3. Buat indeks HNSW
		idxDocs := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_embedding_idx" ON "%s" USING hnsw (embedding vector_cosine_ops);`, defaultTbl, defaultTbl)
		idxCache := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_embedding_idx" ON "%s" USING hnsw (query_embedding vector_cosine_ops);`, cacheTbl, cacheTbl)

		if _, err := pool.Exec(ctx, idxDocs); err != nil {
			return fmt.Errorf("failed creating docs hnsw index: %w", err)
		}
		if _, err := pool.Exec(ctx, idxCache); err != nil {
			return fmt.Errorf("failed creating cache hnsw index: %w", err)
		}
	} else {
		queries := []string{
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
				id SERIAL PRIMARY KEY,
				category VARCHAR(100) NOT NULL DEFAULT 'Umum',
				title VARCHAR(255) NOT NULL,
				content TEXT NOT NULL,
				embedding DOUBLE PRECISION[] NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);`, defaultTbl),
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
				id SERIAL PRIMARY KEY,
				query_text TEXT NOT NULL,
				query_embedding DOUBLE PRECISION[] NOT NULL,
				feedback JSONB NOT NULL,
				matched_sources JSONB DEFAULT '[]'::jsonb,
				hit_count INT DEFAULT 1,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);`, cacheTbl),
		}

		for _, query := range queries {
			if _, err := pool.Exec(ctx, query); err != nil {
				return fmt.Errorf("failed executing schema query (%s): %w", query, err)
			}
		}
	}

	return nil
}

// ResetTable drops a specific table and its corresponding cache table, then recreates them.
func ResetTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	tbl := SanitizeTableName(table)
	cacheTbl := GetCacheTableName(tbl)

	queries := []string{
		fmt.Sprintf(`DROP TABLE IF EXISTS "%s" CASCADE;`, tbl),
		fmt.Sprintf(`DROP TABLE IF EXISTS "%s" CASCADE;`, cacheTbl),
	}

	for _, query := range queries {
		if _, err := pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed executing drop query (%s): %w", query, err)
		}
	}

	return InitSchema(ctx, pool)
}

// ResetDatabase drops the tables and recreates clean schemas.
func ResetDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	return ResetTable(ctx, pool, GetDefaultTable())
}

// InsertDocumentTo saves a document along with its vector embedding into a specified table.
func InsertDocumentTo(ctx context.Context, pool *pgxpool.Pool, table, category, title, content string, embedding pgvector.Vector) (int, error) {
	tbl := SanitizeTableName(table)
	if category == "" {
		category = "Umum"
	}

	var id int
	if HasPgVector {
		query := fmt.Sprintf(`
			INSERT INTO "%s" (category, title, content, embedding)
			VALUES ($1, $2, $3, $4)
			RETURNING id;
		`, tbl)
		err := pool.QueryRow(ctx, query, category, title, content, embedding).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to insert document into %s: %w", tbl, err)
		}
	} else {
		f32s := embedding.Slice()
		f64s := make([]float64, len(f32s))
		for i, v := range f32s {
			f64s[i] = float64(v)
		}
		query := fmt.Sprintf(`
			INSERT INTO "%s" (category, title, content, embedding)
			VALUES ($1, $2, $3, $4)
			RETURNING id;
		`, tbl)
		err := pool.QueryRow(ctx, query, category, title, content, f64s).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to insert document into %s: %w", tbl, err)
		}
	}
	return id, nil
}

// InsertDocument wrapper saving into default table.
func InsertDocument(ctx context.Context, pool *pgxpool.Pool, category, title, content string, embedding pgvector.Vector) (int, error) {
	return InsertDocumentTo(ctx, pool, GetDefaultTable(), category, title, content, embedding)
}

// SearchSimilarIn finds top K most similar documents in a specific table using cosine similarity.
func SearchSimilarIn(ctx context.Context, pool *pgxpool.Pool, table string, queryVec pgvector.Vector, limit int) ([]SearchResult, error) {
	tbl := SanitizeTableName(table)
	var rows pgx.Rows
	var err error

	if HasPgVector {
		query := fmt.Sprintf(`
			SELECT id, category, title, content, 1 - (embedding <=> $1) AS similarity
			FROM "%s"
			ORDER BY embedding <=> $1 ASC
			LIMIT $2;
		`, tbl)
		rows, err = pool.Query(ctx, query, queryVec, limit)
	} else {
		f32s := queryVec.Slice()
		f64s := make([]float64, len(f32s))
		for i, v := range f32s {
			f64s[i] = float64(v)
		}
		query := fmt.Sprintf(`
			SELECT id, category, title, content, cosine_similarity(embedding, $1) AS similarity
			FROM "%s"
			ORDER BY similarity DESC
			LIMIT $2;
		`, tbl)
		rows, err = pool.Query(ctx, query, f64s, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to search similar documents in %s: %w", tbl, err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(&item.ID, &item.Category, &item.Title, &item.Content, &item.Similarity); err != nil {
			return nil, fmt.Errorf("failed scanning search result row: %w", err)
		}
		results = append(results, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	if results == nil {
		results = []SearchResult{}
	}
	return results, nil
}

// SearchSimilar wrapper searching in default table.
func SearchSimilar(ctx context.Context, pool *pgxpool.Pool, queryVec pgvector.Vector, limit int) ([]SearchResult, error) {
	return SearchSimilarIn(ctx, pool, GetDefaultTable(), queryVec, limit)
}

// GetAllDocumentsFrom retrieves all documents from a specific table ordered by ID ascending.
func GetAllDocumentsFrom(ctx context.Context, pool *pgxpool.Pool, table string, limit, offset int) ([]DocumentResponse, error) {
	tbl := SanitizeTableName(table)
	if limit <= 0 {
		limit = 50
	}

	query := fmt.Sprintf(`
		SELECT id, category, title, content, created_at
		FROM "%s"
		ORDER BY id ASC
		LIMIT $1 OFFSET $2;
	`, tbl)
	rows, err := pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch documents from %s: %w", tbl, err)
	}
	defer rows.Close()

	var docs []DocumentResponse
	for rows.Next() {
		var doc DocumentResponse
		if err := rows.Scan(&doc.ID, &doc.Category, &doc.Title, &doc.Content, &doc.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan document row: %w", err)
		}
		docs = append(docs, doc)
	}

	if docs == nil {
		docs = []DocumentResponse{}
	}
	return docs, nil
}

// GetAllDocuments wrapper using default table.
func GetAllDocuments(ctx context.Context, pool *pgxpool.Pool, limit, offset int) ([]DocumentResponse, error) {
	return GetAllDocumentsFrom(ctx, pool, GetDefaultTable(), limit, offset)
}

// GetDocumentByIDFrom retrieves a single document by its primary key ID from a specific table.
func GetDocumentByIDFrom(ctx context.Context, pool *pgxpool.Pool, table string, id int) (*DocumentResponse, error) {
	tbl := SanitizeTableName(table)
	query := fmt.Sprintf(`
		SELECT id, category, title, content, created_at
		FROM "%s"
		WHERE id = $1;
	`, tbl)
	var doc DocumentResponse
	err := pool.QueryRow(ctx, query, id).Scan(&doc.ID, &doc.Category, &doc.Title, &doc.Content, &doc.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// GetDocumentByID wrapper using default table.
func GetDocumentByID(ctx context.Context, pool *pgxpool.Pool, id int) (*DocumentResponse, error) {
	return GetDocumentByIDFrom(ctx, pool, GetDefaultTable(), id)
}

// UpdateDocumentIn updates category, title, content, and vector in a specific table.
func UpdateDocumentIn(ctx context.Context, pool *pgxpool.Pool, table string, id int, category, title, content string, embedding pgvector.Vector) error {
	tbl := SanitizeTableName(table)
	if category == "" {
		category = "Umum"
	}
	query := fmt.Sprintf(`
		UPDATE "%s"
		SET category = $1, title = $2, content = $3, embedding = $4
		WHERE id = $5;
	`, tbl)
	var err error
	var rowsAffected int64
	if HasPgVector {
		tag, execErr := pool.Exec(ctx, query, category, title, content, embedding, id)
		err = execErr
		rowsAffected = tag.RowsAffected()
	} else {
		f32s := embedding.Slice()
		f64s := make([]float64, len(f32s))
		for i, v := range f32s {
			f64s[i] = float64(v)
		}
		tag, execErr := pool.Exec(ctx, query, category, title, content, f64s, id)
		err = execErr
		rowsAffected = tag.RowsAffected()
	}
	if err != nil {
		return fmt.Errorf("failed to update document in %s: %w", tbl, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("document with id %d not found in %s", id, tbl)
	}
	return nil
}

// UpdateDocument wrapper using default table.
func UpdateDocument(ctx context.Context, pool *pgxpool.Pool, id int, category, title, content string, embedding pgvector.Vector) error {
	return UpdateDocumentIn(ctx, pool, GetDefaultTable(), id, category, title, content, embedding)
}

// DeleteDocumentFrom removes a document by its ID from a specific table.
func DeleteDocumentFrom(ctx context.Context, pool *pgxpool.Pool, table string, id int) error {
	tbl := SanitizeTableName(table)
	query := fmt.Sprintf(`DELETE FROM "%s" WHERE id = $1;`, tbl)
	tag, err := pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete document from %s: %w", tbl, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("document with id %d not found in %s", id, tbl)
	}
	return nil
}

// DeleteDocument wrapper using default table.
func DeleteDocument(ctx context.Context, pool *pgxpool.Pool, id int) error {
	return DeleteDocumentFrom(ctx, pool, GetDefaultTable(), id)
}

// CountDocumentsIn returns total number of documents in a specific table.
func CountDocumentsIn(ctx context.Context, pool *pgxpool.Pool, table string) (int64, error) {
	tbl := SanitizeTableName(table)
	var count int64
	err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s";`, tbl)).Scan(&count)
	return count, err
}

// CountDocuments wrapper using default table.
func CountDocuments(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return CountDocumentsIn(ctx, pool, GetDefaultTable())
}

// FindCachedFeedbackIn searches for a previously answered query in a specific cache table.
func FindCachedFeedbackIn(ctx context.Context, pool *pgxpool.Pool, cacheTable string, queryVec pgvector.Vector, threshold float64) (*QueryCacheHit, error) {
	tbl := SanitizeTableName(cacheTable)
	var hit QueryCacheHit
	var feedbackBytes, matchedSourcesBytes []byte
	var err error

	if HasPgVector {
		query := fmt.Sprintf(`
			SELECT id, query_text, feedback, matched_sources, 1 - (query_embedding <=> $1) AS similarity, hit_count
			FROM "%s"
			WHERE 1 - (query_embedding <=> $1) >= $2
			ORDER BY query_embedding <=> $1 ASC
			LIMIT 1;
		`, tbl)
		err = pool.QueryRow(ctx, query, queryVec, threshold).Scan(
			&hit.ID,
			&hit.QueryText,
			&feedbackBytes,
			&matchedSourcesBytes,
			&hit.Similarity,
			&hit.HitCount,
		)
	} else {
		f32s := queryVec.Slice()
		f64s := make([]float64, len(f32s))
		for i, v := range f32s {
			f64s[i] = float64(v)
		}
		query := fmt.Sprintf(`
			SELECT id, query_text, feedback, matched_sources, cosine_similarity(query_embedding, $1) AS similarity, hit_count
			FROM "%s"
			WHERE cosine_similarity(query_embedding, $1) >= $2
			ORDER BY similarity DESC
			LIMIT 1;
		`, tbl)
		err = pool.QueryRow(ctx, query, f64s, threshold).Scan(
			&hit.ID,
			&hit.QueryText,
			&feedbackBytes,
			&matchedSourcesBytes,
			&hit.Similarity,
			&hit.HitCount,
		)
	}
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to search query cache in %s: %w", tbl, err)
	}

	hit.Feedback = feedbackBytes
	hit.MatchedSources = matchedSourcesBytes
	return &hit, nil
}

// FindCachedFeedback wrapper using default cache table.
func FindCachedFeedback(ctx context.Context, pool *pgxpool.Pool, queryVec pgvector.Vector, threshold float64) (*QueryCacheHit, error) {
	return FindCachedFeedbackIn(ctx, pool, GetCacheTableName(GetDefaultTable()), queryVec, threshold)
}

// IncrementCacheHitIn updates the hit counter and timestamp for a cache hit.
func IncrementCacheHitIn(ctx context.Context, pool *pgxpool.Pool, cacheTable string, id int) error {
	tbl := SanitizeTableName(cacheTable)
	query := fmt.Sprintf(`
		UPDATE "%s"
		SET hit_count = hit_count + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`, tbl)
	_, err := pool.Exec(ctx, query, id)
	return err
}

// IncrementCacheHit wrapper using default cache table.
func IncrementCacheHit(ctx context.Context, pool *pgxpool.Pool, id int) error {
	return IncrementCacheHitIn(ctx, pool, GetCacheTableName(GetDefaultTable()), id)
}

// InsertQueryCacheIn saves a query embedding and generated feedback to the specified cache table.
func InsertQueryCacheIn(ctx context.Context, pool *pgxpool.Pool, cacheTable string, queryText string, queryVec pgvector.Vector, feedback any, matchedSources any) error {
	tbl := SanitizeTableName(cacheTable)
	feedbackBytes, err := json.Marshal(feedback)
	if err != nil {
		return fmt.Errorf("failed marshaling feedback for cache: %w", err)
	}

	sourcesBytes, err := json.Marshal(matchedSources)
	if err != nil {
		return fmt.Errorf("failed marshaling matched sources for cache: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO "%s" (query_text, query_embedding, feedback, matched_sources)
		VALUES ($1, $2, $3, $4);
	`, tbl)
	if HasPgVector {
		_, err = pool.Exec(ctx, query, queryText, queryVec, feedbackBytes, sourcesBytes)
	} else {
		f32s := queryVec.Slice()
		f64s := make([]float64, len(f32s))
		for i, v := range f32s {
			f64s[i] = float64(v)
		}
		_, err = pool.Exec(ctx, query, queryText, f64s, feedbackBytes, sourcesBytes)
	}
	if err != nil {
		return fmt.Errorf("failed inserting query cache into %s: %w", tbl, err)
	}
	return nil
}

// InsertQueryCache wrapper using default cache table.
func InsertQueryCache(ctx context.Context, pool *pgxpool.Pool, queryText string, queryVec pgvector.Vector, feedback any, matchedSources any) error {
	return InsertQueryCacheIn(ctx, pool, GetCacheTableName(GetDefaultTable()), queryText, queryVec, feedback, matchedSources)
}
