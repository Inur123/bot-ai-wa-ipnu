package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/gemini"
	"bot-ai-wa-ipnu/internal/handler"
	"bot-ai-wa-ipnu/internal/scheduler"
	"bot-ai-wa-ipnu/internal/whatsapp"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env
	if err := godotenv.Load(); err != nil {
		log.Println("[Main] File .env tidak ditemukan, menggunakan environment variables sistem")
	}

	// Context dengan cancel untuk graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inisialisasi Database
	if err := database.Connect(); err != nil {
		log.Fatalf("[Main] Gagal koneksi database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		log.Fatalf("[Main] Gagal migrasi database: %v", err)
	}

	// Inisialisasi AI (Gemini / Groq / dst)
	if err := gemini.Init(ctx); err != nil {
		log.Fatalf("[PITI] Gagal inisialisasi AI: %v", err)
	}
	defer gemini.Close()

	// Jalankan Scheduler di background
	go scheduler.Run(ctx)

	// Inisialisasi WhatsApp (jika WA_ENABLED=true)
	if os.Getenv("WA_ENABLED") == "true" {
		log.Println("[PITI-WA] Menginisialisasi koneksi WhatsApp...")
		msgChan, err := whatsapp.InitWhatsApp(ctx)
		if err != nil {
			log.Printf("[PITI-WA] ⚠️ Gagal koneksi WA: %v", err)
			log.Println("[PITI-WA] Server tetap berjalan tanpa WhatsApp (mode API)")
		} else {
			defer whatsapp.Disconnect()
			go whatsapp.ListenAndProcess(ctx, msgChan)
		}
	} else {
		log.Println("[PITI-WA] Mode API (WA_ENABLED=false) - gunakan /api/simulate untuk testing")
	}

	// Setup HTTP Router
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", handler.HealthCheck)

	// REST API entries
	mux.HandleFunc("POST /api/entries", handler.CreateEntry)
	mux.HandleFunc("GET /api/entries", handler.ListEntries)
	mux.HandleFunc("GET /api/entries/{id}", handler.GetEntry)
	mux.HandleFunc("POST /api/entries/{id}/feedback", handler.FeedbackEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", handler.DeleteEntry)

	// Simulasi WA (untuk testing tanpa nomor WA)
	mux.HandleFunc("POST /api/simulate", handler.SimulateWAMessage)

	// CORS middleware (untuk akses dari tools/Postman)
	httpHandler := corsMiddleware(mux)

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      httpHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("[Main] Menerima sinyal shutdown...")
		cancel() // hentikan scheduler

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("[Main] Error saat shutdown: %v", err)
		}
		log.Println("[Main] Server berhenti dengan bersih")
	}()

	log.Printf("[Main] ╔══════════════════════════════════════╗")
	log.Printf("[Main] ║     PITI - AI Agent IPNU-IPPNU       ║")
	log.Printf("[Main] ║   Rekan + Rekanita + Magetan + AI    ║")
	log.Printf("[Main] ║     Berjalan di port :%s            ║", port)
	log.Printf("[Main] ╚══════════════════════════════════════╝")
	log.Printf("[Main] Endpoint:")
	log.Printf("[Main]   GET  /health")
	log.Printf("[Main]   POST /api/entries")
	log.Printf("[Main]   GET  /api/entries")
	log.Printf("[Main]   GET  /api/entries/{id}")
	log.Printf("[Main]   POST /api/entries/{id}/feedback")
	log.Printf("[Main]   DELETE /api/entries/{id}")
	log.Printf("[Main]   POST /api/simulate  (test tanpa WA)")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[Main] Server error: %v", err)
	}
}

// corsMiddleware menambahkan header CORS untuk development
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
