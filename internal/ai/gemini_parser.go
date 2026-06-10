package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/knowledge"
	"bot-ai-wa-ipnu/internal/models"
	"bot-ai-wa-ipnu/internal/search"
	"bot-ai-wa-ipnu/internal/timeutil"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

var genaiClient *genai.Client

// InitGemini menginisialisasi Gemini client secara persisten
func InitGemini(ctx context.Context) error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY tidak diset di .env")
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return fmt.Errorf("gagal menginisialisasi Gemini client: %w", err)
	}
	genaiClient = client
	log.Println("[PITI-AI] Menggunakan Gemini AI Provider")
	return nil
}

// CloseGemini menutup Gemini client
func CloseGemini() {
	if genaiClient != nil {
		genaiClient.Close()
	}
}

// ParseMessageGemini mem-parsing pesan natural language menggunakan Gemini API
func ParseMessageGemini(ctx context.Context, rawMessage, userID string) (*models.ParsedEntry, error) {
	if genaiClient == nil {
		return nil, fmt.Errorf("Gemini client belum diinisialisasi, panggil ai.Init() terlebih dahulu")
	}

	now := time.Now()
	systemPrompt := buildSystemPrompt(now)

	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	model := genaiClient.GenerativeModel(modelName)
	model.SetTemperature(0.1)
	model.ResponseMIMEType = "application/json"
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	userPrompt := fmt.Sprintf("Pesan dari user (ID: %s):\n%s", userID, rawMessage)
	resp, err := generateContentWithRetry(ctx, model, genai.Text(userPrompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini API error: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("respons dari Gemini kosong")
	}

	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			sb.WriteString(string(textPart))
		}
	}
	rawJSON := cleanJSONString(sb.String())

	log.Printf("[Gemini] Raw response: %s", rawJSON)

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON dari Gemini: %w\nRaw: %s", err, rawJSON)
	}

	if err := normalizeAndValidateParsedEntry(&parsed, userID, now); err != nil {
		return nil, err
	}

	return &parsed, nil
}

// ParseFeedbackGemini mem-parsing feedback/koreksi menggunakan Gemini API
func ParseFeedbackGemini(ctx context.Context, feedback string, existingEntry *models.Entry) (*models.ParsedEntry, error) {
	if genaiClient == nil {
		return nil, fmt.Errorf("Gemini client belum diinisialisasi")
	}

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

	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	model := genaiClient.GenerativeModel(modelName)
	model.SetTemperature(0.1)
	model.ResponseMIMEType = "application/json"
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	userPrompt := "Pesan feedback/koreksi: " + feedback
	resp, err := generateContentWithRetry(ctx, model, genai.Text(userPrompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini feedback API error: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("respons feedback dari Gemini kosong")
	}

	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			sb.WriteString(string(textPart))
		}
	}
	rawJSON := cleanJSONString(sb.String())

	var parsed models.ParsedEntry
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("gagal unmarshal JSON feedback dari Gemini: %w\nRaw: %s", err, rawJSON)
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

// ChatGemini membalas percakapan biasa menggunakan Gemini API
func ChatGemini(ctx context.Context, message, userID string) (string, error) {
	if genaiClient == nil {
		return "", fmt.Errorf("Gemini client belum diinisialisasi")
	}

	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	var searchContext string
	if detectSearchIntentGemini(ctx, message) {
		searchQuery := formulateSearchQueryGemini(ctx, message)
		log.Printf("[Search-Gemini] Mendeteksi perlunya pencarian internet. Query hasil formulasi: %s", searchQuery)
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
			log.Printf("[Search-Gemini] Berhasil menyisipkan %d hasil pencarian ke konteks AI.", len(results))
		} else {
			log.Printf("[Search-Gemini] Pencarian tidak menghasilkan data atau gagal: %v", err)
		}
	}

	limit := configuredGeminiKnowledgeLimit()
	systemPrompt := buildChatSystemPromptWithLimit(message, limit)
	if dbContext := getRecentAgendasContext(); dbContext != "" {
		systemPrompt += dbContext
	}
	if searchContext != "" {
		systemPrompt += searchContext
	}

	model := genaiClient.GenerativeModel(modelName)
	model.SetTemperature(0.4)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	resp, err := generateContentWithRetry(ctx, model, genai.Text(message))
	if err != nil {
		return "", fmt.Errorf("Gemini Chat API error: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("respons chat dari Gemini kosong")
	}

	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			sb.WriteString(string(textPart))
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

func configuredGeminiKnowledgeLimit() int {
	raw := strings.TrimSpace(os.Getenv("KNOWLEDGE_MAX_CHARS"))
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 15000
}

// cleanJSONString membersihkan markdown code block jika ada
func cleanJSONString(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func normalizeAndValidateParsedEntry(parsed *models.ParsedEntry, userID string, now time.Time) error {
	nowWIB := timeutil.InWIB(now)
	parsed.Type = models.EntryType(strings.TrimSpace(string(parsed.Type)))
	if !isValidEntryType(parsed.Type) {
		return fmt.Errorf("AI menghasilkan type tidak valid: %q", parsed.Type)
	}

	parsed.Content = strings.TrimSpace(parsed.Content)
	if parsed.Content == "" {
		return fmt.Errorf("AI tidak menghasilkan content pesan")
	}

	parsed.Metadata.UserID = strings.TrimSpace(userID)
	parsed.Metadata.Notes = strings.TrimSpace(parsed.Metadata.Notes)
	parsed.Metadata.Tags = normalizeTags(parsed.Metadata.Tags)
	parsed.Metadata.Targets = normalizeTargets(parsed.Metadata.Targets, userID)
	if len(parsed.Metadata.Targets) == 0 {
		return fmt.Errorf("target pesan tidak ditemukan")
	}

	if parsed.Metadata.EventAt != nil {
		eventAt := timeutil.InWIB(*parsed.Metadata.EventAt)
		parsed.Metadata.EventAt = &eventAt
		if parsed.Type == models.EntryTypeReminder && eventAt.Before(nowWIB.Add(-1*time.Minute)) {
			return fmt.Errorf("waktu acara sudah lewat, minta user mengirim tanggal/jam yang lebih jelas")
		}
	}

	if len(parsed.Metadata.Triggers) == 0 {
		parsed.Metadata.Triggers = []models.Trigger{{
			ID:        "t1",
			SendAt:    nowWIB,
			OffsetMin: 0,
			Status:    models.StatusPending,
		}}
	}

	for i := range parsed.Metadata.Triggers {
		trigger := &parsed.Metadata.Triggers[i]
		if strings.TrimSpace(trigger.ID) == "" {
			trigger.ID = fmt.Sprintf("t%d", i+1)
		}
		if trigger.SendAt.IsZero() {
			trigger.SendAt = nowWIB
		} else {
			trigger.SendAt = timeutil.InWIB(trigger.SendAt)
		}
		trigger.Status = models.StatusPending
		trigger.SentAt = nil
		if parsed.Type == models.EntryTypeReminder && trigger.SendAt.Before(nowWIB.Add(-1*time.Minute)) {
			return fmt.Errorf("waktu pengingat sudah lewat, minta user mengirim tanggal/jam yang lebih jelas")
		}
	}

	return nil
}

func isValidEntryType(entryType models.EntryType) bool {
	switch entryType {
	case models.EntryTypeReminder, models.EntryTypeAnnouncement, models.EntryTypePersonalMessage, models.EntryTypeTopic:
		return true
	default:
		return false
	}
}

func normalizeTargets(targets []models.Target, fallbackUserID string) []models.Target {
	normalized := make([]models.Target, 0, len(targets)+1)
	for _, target := range targets {
		target.ID = strings.TrimSpace(target.ID)
		target.Name = strings.TrimSpace(target.Name)
		target.Type = strings.ToLower(strings.TrimSpace(target.Type))

		if target.ID == "" && target.Name != "" {
			target.ID = target.Name
		}
		if target.Name == "" && target.ID != "" {
			target.Name = target.ID
		}
		if target.Type != "group" && target.Type != "personal" {
			target.Type = inferTargetType(target.ID, target.Name)
		}
		if target.ID == "" && target.Name == "" {
			continue
		}
		normalized = append(normalized, target)
	}

	if len(normalized) == 0 && strings.TrimSpace(fallbackUserID) != "" {
		normalized = append(normalized, models.Target{
			ID:   strings.TrimSpace(fallbackUserID),
			Name: strings.TrimSpace(fallbackUserID),
			Type: "personal",
		})
	}

	return normalized
}

func inferTargetType(id, name string) string {
	lower := strings.ToLower(id + " " + name)
	if strings.Contains(lower, "@g.us") || strings.Contains(lower, "grup") || strings.Contains(lower, "group") ||
		strings.Contains(lower, "pac") || strings.Contains(lower, "pc ") || strings.Contains(lower, "pr ") ||
		strings.Contains(lower, "pk ") {
		return "group"
	}
	return "personal"
}

func normalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	normalized := []string{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

// buildSystemPrompt membangun system prompt dengan waktu sekarang untuk parsing
func buildSystemPrompt(now time.Time) string {
	nowWIB := timeutil.InWIB(now)
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
	1. "type" harus berupa "reminder" jika user meminta untuk membuat agenda, jadwal, pengingat, atau menyebut kata "ingatkan"/"pengingat"/"reminder"/"agenda"/"jadwal". Gunakan "announcement" hanya untuk pengumuman/broadcast langsung biasa tanpa trigger pengingat masa depan.
	2. "content" adalah pesan yang akan dikirim, bisa menggunakan {name} untuk personalisasi nama penerima
	3. Jika user menyebut waktu relatif ("besok", "1 jam lagi", "malam ini"), konversi ke waktu absolut dengan format RFC3339 timezone WIB (+07:00)
4. Jika ada beberapa trigger (misal: "1 jam sebelum" dan "30 menit sebelum"), buat array triggers dengan send_at yang berbeda
5. Jika tidak ada trigger waktu, buat 1 trigger dengan send_at = sekarang (dalam timezone +07:00)
6. Jika WA ID tidak diketahui, gunakan nama yang disebut user sebagai "id"
	7. Selalu gunakan offset timezone WIB (+07:00) untuk semua field datetime (misal: 2026-05-31T13:51:00+07:00). JANGAN gunakan UTC/Z.
	8. Selalu kembalikan HANYA JSON, tanpa teks tambahan apapun
	9. Jika pesan user berisi format multi-baris (baris baru, bullet, atau label seperti "Kegiatan:", "Tanggal:") maka isi "content" dengan format tersebut dan jangan ulangi kata perintah seperti "buatkan" atau "kirimkan".
	10. Skill reminder cerdas: pahami pola H-3, H-1, 2 jam sebelum, 30 menit sebelum, "pagi sebelum acara", dan buat trigger terpisah untuk setiap pengingat.
	11. Skill validasi jadwal: jika waktu relatif ambigu, pilih interpretasi paling dekat yang masih masuk akal di masa depan; jangan membuat reminder yang sudah lewat. Jika "malam ini" sudah lewat, gunakan notes untuk menandai perlu konfirmasi.
	12. Skill template IPNU-IPPNU: untuk agenda/rapat/kegiatan, rapikan content dengan unsur acara, hari/tanggal, waktu, tempat, peserta, dan catatan jika disebut user. Jangan mengarang unsur yang tidak disebut.
	13. Skill sapaan personal: gunakan sapaan Rekan/Rekanita/Rekan-Rekanita pada content jika cocok dengan konteks, tetapi tetap ringkas dan pantas untuk pesan organisasi.
	14. Skill anti-halusinasi: jangan menambah nama pengurus, nomor HP, lokasi, tanggal, atau aturan organisasi yang tidak disebut user.
	`, nowWIB.Format("2006-01-02 15:04:05 MST"))
}

func buildChatSystemPromptWithLimit(message string, maxKnowledgeChars int) string {
	knowledgeText := knowledge.Search(message)
	if maxKnowledgeChars > 0 {
		knowledgeText = knowledge.SearchWithLimit(message, maxKnowledgeChars)
	}
	prompt := `Kamu adalah PITI, asisten WhatsApp resmi milik Pimpinan Cabang (PC) IPNU-IPPNU Kabupaten Magetan, Jawa Timur.

TUGAS UTAMA:
- Membantu pengurus organisasi dalam mengelola agenda, memberikan informasi, dan menjawab percakapan santai.
- Asisten Administrasi Pribadi: Membantu membuat draft surat resmi, menyusun undangan rapat organisasi, merapikan notulen, membuat rundown, membuat caption pengumuman, dan memformat pesan agenda/pengumuman secara rapi.
- Pendamping Organisasi: Memahami konteks umum IPNU-IPPNU seperti PC, PAC, PR, PK, Makesta, Lakmud, Rapat Kerja, Pleno, kaderisasi, surat instruksi, dan pengumuman internal.
- Perangkum Grup: Jika user meminta rangkuman chat/notulen, ringkas menjadi keputusan, daftar tugas, penanggung jawab jika disebut, deadline jika disebut, dan hal yang masih perlu dikonfirmasi.
- Pembuat Template: Jika user meminta format administrasi, gunakan format lazim organisasi: nomor/lampiran/perihal untuk surat jika diminta; salam pembuka, isi, penutup; atau struktur acara/tanggal/waktu/tempat/peserta/catatan untuk undangan dan pengumuman.
- Penjaga Jadwal: Saat membahas agenda/reminder, bantu jelaskan tanggal dan jam secara jelas dalam WIB. Jika waktunya ambigu atau sudah lewat, minta konfirmasi singkat.
- Anti-Halusinasi: Jangan mengarang nama pengurus, nomor HP, alamat, aturan organisasi, isi dokumen, tanggal, atau hasil rapat. Jika data tidak tersedia di pesan user atau knowledge, katakan belum ada data yang cukup.
- Toleransi Typo: Pahami bahwa dokumen basis pengetahuan/knowledge mungkin memiliki kesalahan pengetikan (typo) kecil dari penulisnya, seperti kata "sekertaris" yang bermaksud "sekretaris", "bendaharan" yang bermaksud "bendahara", dll. Tetap hubungkan dan jawab data tersebut secara cerdas jika konteksnya cocok.`

	if knowledgeText != "" {
		prompt += "\n\nBASIS PENGETAHUAN TAMBAHAN:\n"
		prompt += "Gunakan daftar file dan potongan paling relevan berikut untuk menjawab. Jika pertanyaan hanya menanyakan data apa saja yang tersedia, jawab berdasarkan DAFTAR FILE KNOWLEDGE TERSEDIA. Pahami juga jika ada typo penulisan pada dokumen (misal kata 'sekertaris' berarti 'sekretaris'). Jika potongan belum cukup untuk menjawab detail tertentu, katakan data belum cukup atau minta kata kunci yang lebih spesifik; jangan mengarang.\n"
		prompt += knowledgeText
	}

	prompt += `

	ATURAN PENTING & PERSONALITY (BAIK, SANGAT RAMAH, HUMORIS & EMOJI SECUKUPNYA):
	1. NAMA: Selalu perkenalkan dirimu sebagai PITI, asisten cerdas nan menggemaskan.
2. NADA & GAYA (LAYAKNYA TEMAN DEKAT): 
   - Harus sangat baik, ramah, super santai, ceria, humoris, dan penuh kehangatan seperti teman dekat (bestie) sendiri. Dilarang keras terdengar kaku, formal, dingin, atau galak!
   - **Gunakan emoji secukupnya saja (sekitar 1 sampai 3 emoji per seluruh pesan)** di tempat yang dirasa paling pas agar pesan tetap terlihat bersih, rapi, dan mudah dibaca (contoh: 😎, 🔥, 🥳, 😜, ✨, 👍, 🚀). Jangan menaruh emoji di setiap baris atau kalimat.
   - Gunakan panggilan khas organisasi IPNU-IPPNU secara fleksibel: panggil lawan bicara dengan "Rekan" (jika laki-laki/IPNU) atau "Rekanita" (jika perempuan/IPPNU). Jika gender tidak diketahui secara pasti, gunakan gabungan "Rekan/Rekanita" agar adil dan sopan. Di dalam chat grup, sapa anggota grup secara keseluruhan dengan "Rekan-Rekanita".
   - Sisipkan guyonan ringan, candaan receh, atau celetukan bahasa Jawa khas Magetan/Jawa Timur (Jawatimuran) biar lebih akrab (contoh: "Walah Rekan/Rekanita...", "Mantep tenan Rekanita! 😎", "Ojo lali yo bestie! 😜", "Siap boss! 🚀").
   - Jadilah pendengar dan asisten yang selalu positif, mendukung, dan siap menghibur Rekan/Rekanita yang sedang lelah mengurus organisasi.
3. KEJUJURAN MUTLAK & DILARANG KERAS MENGARANG (CRITICAL):
   - Jika kamu tidak tahu atau tidak memiliki data yang ditanyakan (seperti lirik lagu, nama pengurus spesifik, nomor HP, koordinat lokasi, atau informasi organisasi lainnya), jawablah dengan jujur, sopan, dan bisa diselingi candaan ringan bahwa kamu tidak mengetahuinya. JANGAN PERNAH mengarang jawaban atau berhalusinasi fakta!
4. INFORMASI KANTOR & SHERLOCK:
   - Jika ditanya "sherlock kantor" atau "share location kantor", jelaskan secara lucu dan sopan bahwa sebagai bot WhatsApp, kamu belum punya kaki untuk share loc peta GPS secara langsung. Sarankan untuk menanyakan ke pengurus/admin grup untuk alamat pastinya.
	5. PENANGANAN SLANG/BAHASA JAWA:
	   - "dong pora" / "pora" / "ora" artinya "paham tidak?" / "tidak tahu". Jawab dengan santai dan kocak (misal: "Nggih Rekan, kulo paham 100%! Otak bot saya langsung connect!").
	   - Jika user berkata "muter-muter" (penjelasannya berbelit-belit), jawab dengan candaan meminta maaf dan berikan ringkasan yang super to-the-point dan lucu.
	6. TEMPLATE ADMIN:
	   - Draft surat: buat struktur rapi, formal, dan siap diedit. Gunakan placeholder seperti [Nomor Surat], [Tanggal], [Tempat] jika data belum diberikan.
	   - Undangan/pengumuman: prioritaskan Acara, Hari/Tanggal, Waktu, Tempat, Peserta, Catatan, dan Narahubung jika disebut.
	   - Notulen/rangkuman: pisahkan Poin Pembahasan, Keputusan, Tindak Lanjut, PIC, dan Deadline jika datanya ada.
	7. FORMAT: Jawab langsung dalam format teks biasa (plain text), singkat, padat, jelas, dan interaktif. Jangan gunakan format JSON atau markdown codeblock.
	8. ANTI-TECHNICAL LANGUAGE (BAHASA MANUSIA):
	   - JANGAN PERNAH menyebut istilah teknis seperti "database", "sistem", "database saya", "sistem saya", "memori bot", "basis data", atau "server" ketika menjelaskan informasi yang tidak/belum Anda ketahui.
	   - Gunakan bahasa yang halus, ramah, dan manusiawi layaknya teman dekat. Sebagai ganti, gunakan kata seperti: "catatan PITI", "ingatan PITI", "buku agenda PITI", "belum masuk ke catatan PITI", atau "PITI belum diinfokan oleh pengurus/admin".`

	return prompt
}

func generateContentWithRetry(ctx context.Context, model *genai.GenerativeModel, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	var resp *genai.GenerateContentResponse
	var err error
	maxRetries := 3
	backoff := 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err = model.GenerateContent(ctx, parts...)
		if err == nil {
			return resp, nil
		}

		errStr := err.Error()
		if strings.Contains(errStr, "429") || strings.Contains(strings.ToLower(errStr), "exhausted") || strings.Contains(strings.ToLower(errStr), "quota") {
			if attempt < maxRetries {
				sleepTime := parseRetryDuration(errStr)
				if sleepTime > 0 {
					log.Printf("[Gemini] Rate limit terlampaui (429). Menunggu sesuai instruksi API selama %v sebelum mencoba kembali (Percobaan %d/%d)...", sleepTime, attempt, maxRetries)
					backoff = sleepTime
				} else {
					log.Printf("[Gemini] Rate limit terlampaui (429). Menunggu %v sebelum mencoba kembali (Percobaan %d/%d)...", backoff, attempt, maxRetries)
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
				if sleepTime == 0 {
					backoff *= 2
				}
				continue
			}
		}
		break
	}
	return nil, err
}

func parseRetryDuration(errStr string) time.Duration {
	idx := strings.Index(errStr, "Please retry in ")
	if idx != -1 {
		sub := errStr[idx+len("Please retry in "):]
		end := strings.Index(sub, "s")
		if end != -1 {
			secondsStr := strings.TrimSpace(sub[:end])
			if secs, err := strconv.ParseFloat(secondsStr, 64); err == nil {
				// Tambahkan buffer 1.5 detik agar benar-benar aman melewati limit window
				return time.Duration((secs+1.5)*float64(time.Second))
			}
		}
	}

	idx2 := strings.Index(errStr, "retry in ")
	if idx2 != -1 {
		sub := errStr[idx2+len("retry in "):]
		end := strings.Index(sub, "s")
		if end != -1 {
			secondsStr := strings.TrimSpace(sub[:end])
			if secs, err := strconv.Atoi(secondsStr); err == nil {
				return time.Duration(secs+2) * time.Second
			}
		}
	}
	return 0
}

func detectSearchIntentGemini(ctx context.Context, message string) bool {
	if genaiClient == nil {
		return false
	}
	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	systemPrompt := `Kamu adalah asisten pintar. Tugasmu adalah mendeteksi apakah pesan dari user membutuhkan informasi terbaru dari internet (misal berita hari ini, lirik lagu viral, cuaca terbaru, fakta aktual yang dinamis, atau informasi luar yang tidak bersifat lokal organisasi).
Jawab HANYA dengan satu kata: "YA" jika butuh mencari ke internet, atau "TIDAK" jika itu pertanyaan umum, obrolan santai, sapaan, atau urusan internal administrasi/jadwal organisasi IPNU-IPPNU.
JANGAN memberikan alasan atau teks tambahan apa pun.`

	model := genaiClient.GenerativeModel(modelName)
	model.SetTemperature(0.0)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	resp, err := generateContentWithRetry(ctx, model, genai.Text(message))
	if err != nil {
		log.Printf("[Search-Intent] Gagal mendeteksi intent search lewat Gemini: %v", err)
		return false
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return false
	}

	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			sb.WriteString(string(textPart))
		}
	}

	ans := strings.ToUpper(strings.TrimSpace(sb.String()))
	log.Printf("[Search-Intent-Gemini] Deteksi untuk '%s': %s", message, ans)
	return strings.Contains(ans, "YA")
}

func formulateSearchQueryGemini(ctx context.Context, message string) string {
	if genaiClient == nil {
		return cleanQueryForSearch(message)
	}
	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	systemPrompt := `Kamu adalah asisten formulasi pencarian. Tugasmu adalah membuat satu query pencarian mesin pencari (search query) yang singkat, padat, dan sangat spesifik berdasarkan pesan user dan konteks percakapan yang diberikan.
Jawab HANYA dengan query pencarian tersebut (misal: "lirik lagu mbg viral" atau "presiden indonesia 2026"). JANGAN berikan tanda kutip, penjelasan, atau teks tambahan apa pun.`

	model := genaiClient.GenerativeModel(modelName)
	model.SetTemperature(0.0)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	resp, err := generateContentWithRetry(ctx, model, genai.Text(message))
	if err != nil {
		log.Printf("[Search-Query-Gemini] Gagal memformulasi query: %v", err)
		return cleanQueryForSearch(message)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return cleanQueryForSearch(message)
	}

	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if textPart, ok := part.(genai.Text); ok {
			sb.WriteString(string(textPart))
		}
	}

	query := strings.TrimSpace(sb.String())
	query = strings.Trim(query, `"'`)
	log.Printf("[Search-Query-Gemini] Formulasi query untuk '%s' -> '%s'", message, query)
	return query
}
