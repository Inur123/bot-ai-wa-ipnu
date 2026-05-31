package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/models"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

var client *genai.Client

// Init menginisialisasi Gemini client
func Init(ctx context.Context) error {
	// Skip init Gemini jika provider lain yang digunakan
	provider := os.Getenv("AI_PROVIDER")
	if provider != "" && provider != "gemini" {
		log.Printf("[Gemini] Skipped - AI_PROVIDER=%s", provider)
		return nil
	}

	if os.Getenv("OFFLINE_MODE") == "true" {
		log.Println("[Gemini-OFFLINE] Mode Offline Aktif. Gemini API tidak digunakan.")
		return nil
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY tidak diset")
	}

	var err error
	client, err = genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return fmt.Errorf("gagal membuat Gemini client: %w", err)
	}
	log.Println("[Gemini] Client berhasil diinisialisasi")
	return nil
}

// ParseMessage mem-parsing pesan natural language menjadi ParsedEntry
func ParseMessage(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	if os.Getenv("OFFLINE_MODE") == "true" {
		log.Println("[Gemini-OFFLINE] Mock parsing untuk: " + rawMessage)
		entryType := models.EntryTypeAnnouncement
		if strings.Contains(strings.ToLower(rawMessage), "reminder") || strings.Contains(strings.ToLower(rawMessage), "ingatkan") {
			entryType = models.EntryTypeReminder
		}

		targetName := "Grup PC IPNU"
		if strings.Contains(rawMessage, "Grup Marketing") {
			targetName = "Grup Marketing"
		}

		// Kirim 10 detik dari sekarang biar langsung kelihatan di scheduler
		sendAt := time.Now().Add(10 * time.Second)

		return &models.ParsedEntry{
			Type:    entryType,
			Content: rawMessage,
			Metadata: models.Metadata{
				Targets: []models.Target{
					{ID: "target-mock-id", Name: targetName, Type: "group"},
				},
				Triggers: []models.Trigger{
					{ID: "t1", SendAt: sendAt, Status: models.StatusPending},
				},
				UserID: userID,
			},
		}, nil
	}

	now := time.Now()
	systemPrompt := buildSystemPrompt(now)

	model := client.GenerativeModel("gemini-2.0-flash")
	model.SetTemperature(0.1)
	model.ResponseMIMEType = "application/json"

	prompt := fmt.Sprintf("%s\n\n---\nPesan dari user (ID: %s):\n%s", systemPrompt, userID, rawMessage)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("gagal generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("respon Gemini kosong")
	}

	rawJSON := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	rawJSON = strings.TrimSpace(rawJSON)

	log.Printf("[Gemini] Raw response: %s", rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal respons Gemini: %w\nRaw: %s", err, rawJSON)
	}

	// Set status awal semua trigger ke pending
	for i := range parsed.Metadata.Triggers {
		if parsed.Metadata.Triggers[i].ID == "" {
			parsed.Metadata.Triggers[i].ID = fmt.Sprintf("t%d", i+1)
		}
		parsed.Metadata.Triggers[i].Status = models.StatusPending
	}

	// Set user_id
	parsed.Metadata.UserID = userID

	return &parsed, nil
}

// ParseFeedback mem-parsing pesan feedback/koreksi dari user
func ParseFeedback(ctx context.Context, feedback string, existingEntry *models.Entry) (*models.ParsedEntry, error) {
	if os.Getenv("OFFLINE_MODE") == "true" {
		log.Println("[Gemini-OFFLINE] Mock feedback update: " + feedback)
		// Kembalikan entry lama dengan modifikasi dummy
		parsed := &models.ParsedEntry{
			Type:     existingEntry.Type,
			Content:  existingEntry.Content + " (Updated offline: " + feedback + ")",
			Metadata: existingEntry.Metadata,
		}
		// Geser trigger ke 20 detik ke depan
		for i := range parsed.Metadata.Triggers {
			parsed.Metadata.Triggers[i].SendAt = time.Now().Add(20 * time.Second)
		}
		return parsed, nil
	}

	now := time.Now()

	existingJSON, _ := json.MarshalIndent(existingEntry, "", "  ")

	prompt := fmt.Sprintf(`
Kamu adalah AI assistant yang membantu update entry pesan/reminder.
Waktu sekarang: %s

Entry yang ada saat ini:
%s

Pesan feedback/koreksi dari user:
%s

Tugas kamu: Update entry tersebut sesuai feedback. Kembalikan HANYA JSON dalam format yang sama dengan entry yang ada.
Pastikan field "type", "content", dan "metadata" selalu ada.
Jangan ubah data yang tidak disebutkan dalam feedback.
`, now.Format("2006-01-02 15:04 MST"), string(existingJSON), feedback)

	model := client.GenerativeModel("gemini-2.0-flash")
	model.SetTemperature(0.1)
	model.ResponseMIMEType = "application/json"

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("gagal generate feedback response: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("respon Gemini kosong")
	}

	rawJSON := strings.TrimSpace(fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0]))

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal feedback response: %w\nRaw: %s", err, rawJSON)
	}

	return &parsed, nil
}

// ChatGemini membalas percakapan biasa menggunakan Gemini API
func ChatGemini(ctx context.Context, message, userID string) (string, error) {
	if os.Getenv("OFFLINE_MODE") == "true" {
		return "Maaf, mode offline aktif. Saya tidak bisa menjawab sekarang.", nil
	}

	if client == nil {
		return "", fmt.Errorf("Gemini client belum diinisialisasi")
	}

	systemPrompt := "Kamu adalah PITI, asisten AI IPNU-IPPNU Magetan. Jawab singkat, jelas, ramah, dan dalam Bahasa Indonesia. Jangan gunakan format JSON."
	model := client.GenerativeModel("gemini-2.0-flash")
	model.SetTemperature(0.4)
	model.ResponseMIMEType = "text/plain"

	prompt := fmt.Sprintf("%s\n\nPesan dari user:\n%s", systemPrompt, message)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("gagal generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("respon Gemini kosong")
	}

	return strings.TrimSpace(fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])), nil
}

// Close menutup Gemini client
func Close() {
	if client != nil {
		client.Close()
	}
}

func wibTime(t time.Time) time.Time {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	return t.In(loc)
}

// buildSystemPrompt membangun system prompt dengan waktu sekarang
func buildSystemPrompt(now time.Time) string {
	nowWIB := wibTime(now)
	return fmt.Sprintf(`
Kamu adalah PITI (Rekan + Rekanita + Magetan + Intelligent AI), asisten AI cerdas milik IPNU-IPPNU Magetan untuk sistem messaging dan reminder otomatis.
Waktu sekarang: %s (WIB / UTC+7).

Tugasmu: Parsing pesan natural language dari pengurus/admin menjadi JSON terstruktur.

OUTPUT FORMAT (harus valid JSON):
{
  "type": "<reminder|announcement|personal_message|topic>",
  "content": "<isi pesan yang akan dikirim ke target>",
  "metadata": {
    "targets": [
      {"id": "<WA ID atau nama grup>", "name": "<nama tampilan>", "type": "<group|personal>"}
    ],
    "triggers": [
      {"id": "t1", "send_at": "<RFC3339 datetime dengan offset +07:00>", "offset_min": <menit sebelum event, 0 jika tidak ada>, "status": "pending"}
    ],
    "tags": ["<tag opsional>"],
    "user_id": "",
    "notes": "<catatan tambahan opsional>",
    "event_at": "<RFC3339 datetime event utama dengan offset +07:00, atau null jika tidak ada>"
  }
}

ATURAN:
1. "type" harus salah satu dari: reminder, announcement, personal_message, topic
2. "content" adalah pesan yang akan dikirim, bisa menggunakan {name} untuk personalisasi nama penerima
3. Jika user menyebut waktu relatif ("besok", "1 jam lagi", "malam ini"), konversi ke waktu absolut dengan format RFC3339 timezone WIB (+07:00)
4. Jika ada beberapa trigger (misal: "1 jam sebelum" dan "30 menit sebelum"), buat array triggers dengan send_at yang berbeda
5. Jika tidak ada trigger waktu, buat 1 trigger dengan send_at = sekarang (dalam timezone +07:00)
6. Jika WA ID tidak diketahui, gunakan nama yang disebut user sebagai "id"
7. Selalu gunakan offset timezone WIB (+07:00) untuk semua field datetime (misal: 2026-05-31T13:51:00+07:00). JANGAN gunakan UTC/Z.
8. Selalu kembalikan HANYA JSON, tanpa teks tambahan apapun
9. Jika pesan user berisi format multi-baris (baris baru, bullet, atau label seperti "Kegiatan:", "Tanggal:") maka isi "content" dengan format tersebut dan jangan ulangi kata perintah seperti "buatkan" atau "kirimkan".
`, nowWIB.Format("2006-01-02 15:04:05 MST"))
}
