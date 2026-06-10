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

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/models"
	"bot-ai-wa-ipnu/internal/search"
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

	var searchContext string
	if detectSearchIntent(ctx, message) {
		searchQuery := formulateSearchQuery(ctx, message)
		log.Printf("[Search] Mendeteksi perlunya pencarian internet. Query hasil formulasi: %s", searchQuery)
		results, err := search.YahooSearch(searchQuery)
		if err == nil && len(results) > 0 {
			var sb strings.Builder
			sb.WriteString("\n\nBerikut adalah hasil pencarian internet terkini yang valid untuk membantu Anda menjawab pertanyaan user (gunakan info ini jika relevan):\n")
			for i, r := range results {
				if i >= 4 {
					break
				}
				sb.WriteString(fmt.Sprintf("- Judul: %s\n  Link/URL: %s\n  Info: %s\n", r.Title, r.URL, r.Snippet))
			}
			searchContext = sb.String()
			log.Printf("[Search] Berhasil menyisipkan %d hasil pencarian ke konteks AI.", len(results))
		} else {
			log.Printf("[Search] Pencarian tidak menghasilkan data atau gagal: %v", err)
		}
	}

	limit := configuredOpenAIKnowledgeLimit()
	systemPrompt := buildChatSystemPromptWithLimit(message, limit)
	if dbContext := getRecentAgendasContext(); dbContext != "" {
		systemPrompt += dbContext
	}
	if searchContext != "" {
		systemPrompt += searchContext
	}

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

func detectSearchIntent(ctx context.Context, message string) bool {
	baseURL := getOpenAIBaseURL()
	modelName := getOpenAIModel()
	apiKey := os.Getenv("OPENAI_API_KEY")

	systemPrompt := `Kamu adalah asisten pintar. Tugasmu adalah mendeteksi apakah pesan dari user membutuhkan informasi terbaru dari internet (misal berita hari ini, lirik lagu viral, cuaca terbaru, fakta aktual yang dinamis, atau informasi luar yang tidak bersifat lokal organisasi).
Jawab HANYA dengan satu kata: "YA" jika butuh mencari ke internet, atau "TIDAK" jika itu pertanyaan umum, obrolan santai, sapaan, atau urusan internal administrasi/jadwal organisasi IPNU-IPPNU.
JANGAN memberikan alasan atau teks tambahan apa pun.`

	reqBody := map[string]interface{}{
		"model":       modelName,
		"temperature": 0.0,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"stream": false,
	}

	resp, err := callOpenAI(ctx, baseURL, apiKey, reqBody)
	if err != nil {
		log.Printf("[Search-Intent] Gagal mendeteksi intent search: %v", err)
		return false
	}

	ans := strings.ToUpper(strings.TrimSpace(resp))
	log.Printf("[Search-Intent] Deteksi untuk '%s': %s", message, ans)
	return strings.Contains(ans, "YA")
}

func formulateSearchQuery(ctx context.Context, message string) string {
	baseURL := getOpenAIBaseURL()
	modelName := getOpenAIModel()
	apiKey := os.Getenv("OPENAI_API_KEY")

	systemPrompt := `Kamu adalah asisten formulasi pencarian. Tugasmu adalah membuat satu query pencarian mesin pencari (search query) yang singkat, padat, dan sangat spesifik berdasarkan pesan user dan konteks percakapan yang diberikan.
Jawab HANYA dengan query pencarian tersebut (misal: "lirik lagu mbg viral" atau "presiden indonesia 2026"). JANGAN berikan tanda kutip, penjelasan, atau teks tambahan apa pun.`

	reqBody := map[string]interface{}{
		"model":       modelName,
		"temperature": 0.0,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": message},
		},
		"stream": false,
	}

	resp, err := callOpenAI(ctx, baseURL, apiKey, reqBody)
	if err != nil {
		log.Printf("[Search-Query] Gagal memformulasi query: %v", err)
		return cleanQueryForSearch(message)
	}

	query := strings.TrimSpace(resp)
	query = strings.Trim(query, `"'`)
	log.Printf("[Search-Query] Formulasi query untuk '%s' -> '%s'", message, query)
	return query
}

func cleanQueryForSearch(message string) string {
	if strings.HasPrefix(message, "[Konteks:") {
		idx := strings.Index(message, "]\n")
		if idx != -1 {
			return strings.TrimSpace(message[idx+2:])
		}
		idx2 := strings.Index(message, "]")
		if idx2 != -1 {
			return strings.TrimSpace(message[idx2+1:])
		}
	}
	return message
}

func getRecentAgendasContext() string {
	entries, err := database.GetAllEntries(15, 0)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\nDAFTAR AGENDA/REMINDER TERBARU DI DATABASE (Gunakan info ini untuk menjawab pertanyaan terkait agenda/reminder):\n")
	for _, e := range entries {
		triggersStr := ""
		for _, t := range e.Metadata.Triggers {
			triggersStr += fmt.Sprintf("%s (%s), ", t.SendAt.Format("02 Jan 2006 15:04 WIB"), t.Status)
		}
		triggersStr = strings.TrimSuffix(triggersStr, ", ")
		sb.WriteString(fmt.Sprintf("- ID: #%d | Tipe: %s | Status: %s | Konten: %s | Jadwal Kirim: %s\n",
			e.ID, e.Type, e.Status, e.Content, triggersStr))
	}
	return sb.String()
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
