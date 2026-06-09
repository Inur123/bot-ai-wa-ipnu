package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/models"
	"bot-ai-wa-ipnu/internal/timeutil"
)

// InitOpenAI menginisialisasi client OpenAI-compatible
func InitOpenAI(ctx context.Context) error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY tidak diset di .env")
	}
	log.Println("[PITI-AI] Menggunakan OpenAI-Compatible AI Provider (9Router Proxy)")
	return nil
}

// CloseOpenAI membersihkan resource
func CloseOpenAI() {}

// ParseMessageOpenAI mem-parsing pesan natural language menggunakan OpenAI API
func ParseMessageOpenAI(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	now := time.Now()
	systemPrompt := buildSystemPrompt(now)

	baseURL := getOpenAIBaseURL()
	modelName := getOpenAIModel()
	apiKey := os.Getenv("OPENAI_API_KEY")

	reqBody := map[string]interface{}{
		"model":       modelName,
		"temperature": 0.1,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("Pesan dari user (ID: %s):\n%s", userID, rawMessage)},
		},
		"response_format": map[string]string{"type": "json_object"},
		"stream":          false,
	}

	rawJSON, err := callOpenAI(ctx, baseURL, apiKey, reqBody)
	if err != nil {
		return nil, err
	}

	log.Printf("[OpenAI] Raw response: %s", rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON dari OpenAI: %w\nRaw: %s", err, rawJSON)
	}

	if err := normalizeAndValidateParsedEntry(&parsed, userID, now); err != nil {
		return nil, err
	}

	return &parsed, nil
}

// ParseFeedbackOpenAI mem-parsing feedback/koreksi menggunakan OpenAI API
func ParseFeedbackOpenAI(ctx context.Context, feedback string, existingEntry *models.Entry) (*models.ParsedEntry, error) {
	nowWIB := timeutil.Now()
	existingJSON, _ := json.MarshalIndent(existingEntry, "", "  ")

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
7. Skill koreksi natural: pahami "mundurkan 30 menit", "majukan 1 jam", "ganti target", "hapus pengingat H-1", "tambah pengingat 2 jam sebelum", dan update hanya bagian yang diminta.
8. Skill reminder cerdas: jika event_at berubah, semua trigger berbasis offset wajib ikut berubah konsisten.
9. Skill validasi jadwal: jangan membuat event_at/send_at yang sudah lewat. Jika feedback ambigu atau waktu relatif sudah lewat, pertahankan data lama dan isi notes bahwa perlu konfirmasi.
10. Skill anti-halusinasi: jangan menambah target, tanggal, tempat, nomor, atau nama pengurus yang tidak disebut di feedback maupun entry lama.
`, nowWIB.Format("2006-01-02 15:04 WIB"), string(existingJSON))

	baseURL := getOpenAIBaseURL()
	modelName := getOpenAIModel()
	apiKey := os.Getenv("OPENAI_API_KEY")

	reqBody := map[string]interface{}{
		"model":       modelName,
		"temperature": 0.1,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Pesan feedback/koreksi: " + feedback},
		},
		"response_format": map[string]string{"type": "json_object"},
		"stream":          false,
	}

	rawJSON, err := callOpenAI(ctx, baseURL, apiKey, reqBody)
	if err != nil {
		return nil, err
	}

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON feedback dari OpenAI: %w\nRaw: %s", err, rawJSON)
	}

	userID := existingEntry.Metadata.UserID
	if userID == "" {
		userID = "api-user"
	}
	if err := normalizeAndValidateParsedEntry(&parsed, userID, nowWIB); err != nil {
		return nil, err
	}

	return &parsed, nil
}

// ChatOpenAI membalas percakapan biasa menggunakan OpenAI API
func ChatOpenAI(ctx context.Context, message, userID string) (string, error) {
	baseURL := getOpenAIBaseURL()
	modelName := getOpenAIModel()
	apiKey := os.Getenv("OPENAI_API_KEY")

	limit := configuredOpenAIKnowledgeLimit()
	systemPrompt := buildChatSystemPromptWithLimit(message, limit)

	reqBody := map[string]interface{}{
		"model":       modelName,
		"temperature": 0.4,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"stream": false,
	}

	respText, err := callOpenAI(ctx, baseURL, apiKey, reqBody)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(respText), nil
}

func getOpenAIBaseURL() string {
	val := os.Getenv("OPENAI_BASE_URL")
	if val == "" {
		return "https://9router.zainur.biz.id/v1"
	}
	return val
}

func getOpenAIModel() string {
	val := os.Getenv("OPENAI_MODEL")
	if val == "" {
		return "gemini/gemini-2.5-flash"
	}
	return val
}

func configuredOpenAIKnowledgeLimit() int {
	raw := strings.TrimSpace(os.Getenv("KNOWLEDGE_MAX_CHARS"))
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 15000
}

func callOpenAI(ctx context.Context, baseURL, apiKey string, reqBody map[string]interface{}) (string, error) {
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gagal marshal request body: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseURL, "/"))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("gagal membuat request HTTP: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal mengirim request ke OpenAI-Compatible API: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gagal membaca respons API: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI-Compatible API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	cleanJSON := cleanJSONResponse(string(respBytes))

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(cleanJSON), &apiResp); err != nil {
		return "", fmt.Errorf("gagal unmarshal respons API: %w\nRaw: %s", err, cleanJSON)
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("respons API kosong")
	}

	return apiResp.Choices[0].Message.Content, nil
}

func cleanJSONResponse(raw string) string {
	raw = strings.TrimSpace(raw)
	lastBrace := strings.LastIndex(raw, "}")
	if lastBrace != -1 {
		return raw[:lastBrace+1]
	}
	return raw
}
