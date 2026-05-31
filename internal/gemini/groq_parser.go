package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/models"
)

const groqAPIURL = "https://api.groq.com/openai/v1/chat/completions"

// ParseMessageGroq mem-parsing pesan natural language menggunakan Groq API (Llama 3.1)
func ParseMessageGroq(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY tidak diset di .env")
	}

	now := time.Now()
	systemPrompt := buildSystemPrompt(now)

	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		model = "llama-3.1-8b-instant" // cepat, gratis, pintar
	}

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("Pesan dari user (ID: %s):\n%s", userID, rawMessage)},
		},
		"temperature": 0.1,
		"max_tokens":  1024,
	}

	rawJSON, err := callGroq(ctx, groqKey, reqBody)
	if err != nil {
		return nil, err
	}

	log.Printf("[Groq] Raw response: %s", rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON dari Groq: %w\nRaw: %s", err, rawJSON)
	}

	// Set status awal trigger ke pending
	for i := range parsed.Metadata.Triggers {
		if parsed.Metadata.Triggers[i].ID == "" {
			parsed.Metadata.Triggers[i].ID = fmt.Sprintf("t%d", i+1)
		}
		parsed.Metadata.Triggers[i].Status = models.StatusPending
	}
	parsed.Metadata.UserID = userID

	return &parsed, nil
}

// ParseFeedbackGroq mem-parsing feedback/koreksi menggunakan Groq API
func ParseFeedbackGroq(ctx context.Context, feedback string, existingEntry *models.Entry) (*models.ParsedEntry, error) {
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY tidak diset di .env")
	}

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	nowWIB := time.Now().In(loc)
	existingJSON, _ := json.MarshalIndent(existingEntry, "", "  ")

	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		model = "llama-3.1-8b-instant"
	}

	systemPrompt := fmt.Sprintf(`
Kamu adalah PITI, AI assistant IPNU-IPPNU Magetan untuk update entry reminder/pengumuman.
Waktu sekarang: %s (WIB / UTC+7)

Entry yang ada saat ini (JSON):
%s

TUGAS: Update entry sesuai feedback user. Kembalikan HANYA JSON valid, tanpa teks lain.

ATURAN PENTING:
1. Jika user mengubah waktu event/rapat (event_at), hitung ulang send_at pada semua triggers:
   - send_at = event_at dikurangi offset_min menit
   - Jika offset_min tidak disebutkan, pertahankan offset_min yang lama
   - Contoh: event_at=16:00, offset_min=30 → send_at=15:30
2. Jika user menyebut "ingatkan jam X", artinya send_at = jam X (offset_min = 0)
3. Jika user menyebut "ingatkan X menit sebelum", hitung send_at dari event_at - X menit
4. Gunakan format RFC3339 dengan timezone WIB (+07:00) untuk semua datetime (misal: 2026-05-31T13:51:00+07:00). JANGAN gunakan UTC/Z.
5. Pertahankan semua field yang tidak disebutkan dalam feedback
6. Field "status" triggers selalu "pending" setelah update
`, nowWIB.Format("2006-01-02 15:04 WIB"), string(existingJSON))

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Pesan feedback/koreksi: " + feedback},
		},
		"temperature": 0.1,
		"max_tokens":  1024,
	}

	rawJSON, err := callGroq(ctx, groqKey, reqBody)
	if err != nil {
		return nil, err
	}

	rawJSON = cleanJSONString(rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON feedback dari Groq: %w\nRaw: %s", err, rawJSON)
	}

	return &parsed, nil
}

// ChatGroq membalas percakapan biasa menggunakan Groq API
func ChatGroq(ctx context.Context, message, userID string) (string, error) {
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		return "", fmt.Errorf("GROQ_API_KEY tidak diset di .env")
	}

	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		model = "llama-3.1-8b-instant"
	}

	systemPrompt := "Kamu adalah PITI, asisten AI IPNU-IPPNU Magetan. Jawab singkat, jelas, ramah, dan dalam Bahasa Indonesia. Jangan gunakan format JSON."

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"temperature": 0.4,
		"max_tokens":  400,
	}

	respText, err := callGroq(ctx, groqKey, reqBody)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(respText), nil
}

// callGroq mengirim request ke Groq API dan mengembalikan content string
func callGroq(ctx context.Context, apiKey string, reqBody map[string]interface{}) (string, error) {
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gagal marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", groqAPIURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("gagal membuat HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal mengirim request ke Groq: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gagal membaca respons Groq: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Groq API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("gagal unmarshal respons Groq: %w\nRaw: %s", err, string(respBytes))
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("respons Groq kosong")
	}

	return strings.TrimSpace(cleanJSONString(chatResp.Choices[0].Message.Content)), nil
}
