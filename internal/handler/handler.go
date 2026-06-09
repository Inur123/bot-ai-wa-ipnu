package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/ai"
	"bot-ai-wa-ipnu/internal/models"
)

// getAIProvider mengembalikan provider AI yang aktif
func getAIProvider() string {
	p := os.Getenv("AI_PROVIDER")
	if p == "" {
		return "openai"
	}
	return p
}

// Response standar API
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIResponse{Success: false, Error: msg})
}

// ───────────────────────────────────────────────
// POST /api/entries
// Membuat entry baru dari pesan natural language
// ───────────────────────────────────────────────
func CreateEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req models.CreateEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body tidak valid: "+err.Error())
		return
	}
	if req.RawMessage == "" {
		writeError(w, http.StatusBadRequest, "raw_message tidak boleh kosong")
		return
	}
	if req.UserID == "" {
		req.UserID = "api-user"
	}

	ctx := r.Context()
	parsed, err := ai.Parse(ctx, req.RawMessage, req.UserID)
	if err != nil {
		log.Printf("[Handler] Error parsing: %v", err)
		writeError(w, http.StatusInternalServerError, "gagal parsing pesan: "+err.Error())
		return
	}

	entry := &models.Entry{
		Type:     parsed.Type,
		Content:  parsed.Content,
		Metadata: parsed.Metadata,
		Status:   models.StatusPending,
	}

	id, err := database.SaveEntry(entry)
	if err != nil {
		log.Printf("[Handler] Error save: %v", err)
		writeError(w, http.StatusInternalServerError, "gagal menyimpan entry: "+err.Error())
		return
	}

	entry.ID = id
	log.Printf("[Handler] Entry #%d berhasil dibuat (type: %s)", id, entry.Type)

	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Message: "Entry berhasil dibuat",
		Data:    entry,
	})
}

// ───────────────────────────────────────────────
// GET /api/entries
// Mengambil semua entry
// ───────────────────────────────────────────────
func ListEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	entries, err := database.GetAllEntries(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gagal mengambil entries: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    entries,
	})
}

// ───────────────────────────────────────────────
// GET /api/entries/{id}
// Mengambil satu entry
// ───────────────────────────────────────────────
func GetEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID tidak valid")
		return
	}

	entry, err := database.GetEntryByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "entry tidak ditemukan")
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: entry})
}

// ───────────────────────────────────────────────
// POST /api/entries/{id}/feedback
// Koreksi / reschedule entry via natural language
// ───────────────────────────────────────────────
func FeedbackEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID tidak valid")
		return
	}

	var req struct {
		Feedback string `json:"feedback"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body tidak valid")
		return
	}
	if req.Feedback == "" {
		writeError(w, http.StatusBadRequest, "feedback tidak boleh kosong")
		return
	}

	existing, err := database.GetEntryByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "entry tidak ditemukan")
		return
	}

	ctx := r.Context()
	updated, err := ai.ParseFeedbackAuto(ctx, req.Feedback, existing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gagal parsing feedback: "+err.Error())
		return
	}

	// Reset semua trigger ke pending untuk pengiriman ulang
	for i := range updated.Metadata.Triggers {
		updated.Metadata.Triggers[i].Status = models.StatusPending
		updated.Metadata.Triggers[i].SentAt = nil
	}

	if err := database.UpdateEntryMetadata(id, updated.Metadata); err != nil {
		writeError(w, http.StatusInternalServerError, "gagal update entry: "+err.Error())
		return
	}

	// Juga update content jika berubah
	if updated.Content != existing.Content {
		database.DB.Exec(`UPDATE entries SET content = $1 WHERE id = $2`, updated.Content, id)
	}

	result, _ := database.GetEntryByID(id)

	log.Printf("[Handler] Entry #%d diupdate via feedback", id)
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: "Entry berhasil diupdate",
		Data:    result,
	})
}

// ───────────────────────────────────────────────
// DELETE /api/entries/{id}
// Cancel / hapus entry
// ───────────────────────────────────────────────
func DeleteEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID tidak valid")
		return
	}

	if err := database.DeleteEntry(id); err != nil {
		writeError(w, http.StatusInternalServerError, "gagal menghapus entry: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: "Entry dibatalkan"})
}

// ───────────────────────────────────────────────
// GET /health
// Health check endpoint
// ───────────────────────────────────────────────
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"service":  "PITI",
		"fullname": "Rekan + Rekanita + Magetan + Intelligent AI",
		"version":  "1.0.0",
		"provider": getAIProvider(),
	})
}

// ───────────────────────────────────────────────
// POST /api/simulate
// Simulasi terima pesan WA (untuk testing tanpa WA)
// ───────────────────────────────────────────────
func SimulateWAMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		From    string `json:"from"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request tidak valid")
		return
	}

	log.Printf("[Simulate] Pesan dari %s: %s", req.From, req.Message)

	ctx := context.Background()
	parsed, err := ai.Parse(ctx, req.Message, req.From)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gagal parsing: "+err.Error())
		return
	}

	entry := &models.Entry{
		Type:     parsed.Type,
		Content:  parsed.Content,
		Metadata: parsed.Metadata,
		Status:   models.StatusPending,
	}

	id, err := database.SaveEntry(entry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gagal menyimpan: "+err.Error())
		return
	}
	entry.ID = id

	writeJSON(w, http.StatusCreated, APIResponse{
		Success: true,
		Message: "Pesan diproses dan entry dibuat",
		Data:    entry,
	})
}
