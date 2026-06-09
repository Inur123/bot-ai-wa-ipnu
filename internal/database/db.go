package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// DB adalah instance global database
var DB *sql.DB

// Connect membuka koneksi ke PostgreSQL
func Connect() error {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_USER", "postgres"),
		getEnv("DB_PASSWORD", ""),
		getEnv("DB_NAME", "piti_agent"),
	)

	var err error
	DB, err = sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("gagal membuka koneksi DB: %w", err)
	}

	if err = DB.Ping(); err != nil {
		return fmt.Errorf("gagal ping DB: %w", err)
	}

	log.Println("[DB] Koneksi berhasil ke PostgreSQL")
	return nil
}

// Migrate menjalankan DDL untuk membuat tabel jika belum ada
func Migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS entries (
		id         SERIAL PRIMARY KEY,
		type       TEXT NOT NULL,
		content    TEXT NOT NULL,
		metadata   JSONB NOT NULL DEFAULT '{}',
		status     TEXT NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS idx_entries_status ON entries(status);
	CREATE INDEX IF NOT EXISTS idx_entries_type   ON entries(type);
	CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at DESC);
	`

	if _, err := DB.Exec(query); err != nil {
		return fmt.Errorf("gagal migrasi DB: %w", err)
	}

	log.Println("[DB] Migrasi selesai")
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
