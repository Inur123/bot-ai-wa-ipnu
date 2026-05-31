package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/gemini"
	"bot-ai-wa-ipnu/internal/models"
)

// kata-kata yang menandakan pesan adalah koreksi/update entry sebelumnya
var correctionKeywords = []string{
	"ubah", "ganti", "koreksi", "edit", "update",
	"ingatkan", "jadwalkan ulang", "reschedule",
	"batalkan", "cancel", "hapus",
	"waktunya", "jamnya", "tanggalnya", "jadwalnya",
	"tadi", "yang tadi", "sebelumnya", "reminder tadi",
}

// isCorrectionIntent mendeteksi apakah pesan adalah koreksi entry sebelumnya
func isCorrectionIntent(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))

	// Jika diawali kata-kata pembuatan, prioritaskan sebagai buat baru bukan koreksi
	creationPrefixes := []string{
		"buat", "buatkan", "buatkamn", "tambah", "tambahkan", "kirim", "kirimkan", "tulis", "tuliskan",
		"reminder", "pengumuman", "pesan", "schedule", "tolong buat", "tolong buatkan",
	}
	for _, prefix := range creationPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			return false
		}
	}

	// Jika mengandung kata rujukan spesifik ke entry sebelumnya, prioritaskan sebagai koreksi
	refKeywords := []string{
		"pengingatnya", "kirimnya", "pesannya", "di jam", "jadi jam", "ubah jam", "ganti jam",
		"jamnya", "waktunya", "tanggalnya", "jadwalnya", "tadi", "sebelumnya",
	}
	for _, ref := range refKeywords {
		if strings.Contains(lower, ref) {
			return true
		}
	}

	for _, kw := range correctionKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ListenAndProcess memproses semua pesan masuk dari WA secara real-time
func ListenAndProcess(ctx context.Context, msgChan <-chan *IncomingMessage) {
	log.Println("[PITI-WA] Siap menerima pesan dari WhatsApp...")

	triggerPrefix := os.Getenv("WA_TRIGGER_PREFIX")
	if triggerPrefix == "" {
		triggerPrefix = "@PITI"
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[PITI-WA] Listener berhenti")
			return
		case msg := <-msgChan:
			if msg.Message == "" {
				continue
			}

			// Grup: hanya proses jika ada prefix @PITI
			hasPrefix := strings.HasPrefix(msg.Message, triggerPrefix)
			if msg.IsGroup && !hasPrefix && !msg.Mentioned {
				log.Printf("[PITI-WA] Abaikan pesan grup (no mention/prefix): %s", msg.Message)
				continue
			}

			// Bersihkan prefix
			cleanMsg := msg.Message
			if hasPrefix {
				cleanMsg = strings.TrimPrefix(cleanMsg, triggerPrefix)
			}
			cleanMsg = strings.TrimSpace(cleanMsg)
			if msg.IsGroup && msg.Mentioned && !hasPrefix {
				cleanMsg = stripLeadingMention(cleanMsg)
			}

			log.Printf("[PITI-WA] Pesan dari %s: %s", msg.From, cleanMsg)

			go func(m *IncomingMessage, text string, mentioned bool) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[PITI-WA] ⚠️ Panic recovered: %v", r)
					}
				}()
				processConversational(ctx, m.From, m.ChatID, text, mentioned, m.IsGroup)
			}(msg, cleanMsg, msg.IsGroup && (hasPrefix || msg.Mentioned))
		}
	}
}

// processConversational memproses pesan dengan logika percakapan:
// - Format #ID → koreksi entry spesifik
// - Kata koreksi terdeteksi → update entry terakhir user otomatis
// - Perintah baru → buat entry baru
func processConversational(ctx context.Context, from, chatID, message string, isMention, isGroup bool) {
	message = autocorrectKeywords(message)

	if !isAllowedUser(from) {
		if !isGroup {
			_ = Send(ctx, chatID, "Maaf, hanya Zainur yang bisa melakukan chat secara pribadi dengan saya")
			return
		}
		if !isMention {
			return
		}
	}

	if sendScheduleResponse(ctx, chatID, message) {
		return
	}

	if isMention {
		processMentionedMessage(ctx, from, chatID, message)
		return
	}

	// Format lama #ID tetap didukung
	if strings.HasPrefix(message, "#") {
		processFeedbackByID(ctx, from, chatID, message)
		return
	}

	// Deteksi koreksi percakapan
	if isCorrectionIntent(message) {
		lastEntry, err := database.GetLastEntryByUser(from)
		if err == nil && lastEntry != nil {
			log.Printf("[PITI-WA] Koreksi terdeteksi → update entry #%d milik %s", lastEntry.ID, from)
			applyConversationalFeedback(ctx, from, chatID, message, lastEntry)
			return
		}
		log.Printf("[PITI-WA] Koreksi terdeteksi tapi tidak ada entry sebelumnya, buat entry baru")
	}

	// Perintah baru
	processNewEntry(ctx, from, chatID, message)
}

// processMentionedMessage membalas langsung tanpa template jika bot disebut di grup.
func processMentionedMessage(ctx context.Context, from, chatID, message string) {
	if sendScheduleResponse(ctx, chatID, message) {
		return
	}
	reply, err := gemini.ChatReply(ctx, message, from)
	if err != nil {
		log.Printf("[PITI-WA] Error parsing mention: %v", err)
		_ = Send(ctx, chatID, "⚠️ PITI: Maaf, saya belum paham. Bisa dijelaskan lagi?")
		return
	}

	content := strings.TrimSpace(reply)
	content = scrubChatReply(content)
	if content == "" {
		_ = Send(ctx, chatID, "⚠️ PITI: Maaf, saya belum paham. Bisa dijelaskan lagi?")
		return
	}

	_ = Send(ctx, chatID, content)
}

func scrubChatReply(message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "halo id:") {
		trimmed = strings.TrimSpace(trimmed[len("Halo ID:"):])
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	if looksLikeJID(fields[0]) {
		trimmed = strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	return strings.TrimSpace(trimmed)
}

func looksLikeJID(token string) bool {
	lower := strings.ToLower(token)
	if strings.HasSuffix(lower, "@lid") || strings.HasSuffix(lower, "@s.whatsapp.net") || strings.HasSuffix(lower, "@g.us") {
		return true
	}
	return false
}

func stripLeadingMention(message string) string {
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return message
	}
	if strings.HasPrefix(fields[0], "@") {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return message
}

const allowedUserEnv = "ADMIN_WA_PHONE"

func isAllowedUser(jid string) bool {
	allowed := strings.TrimSpace(os.Getenv(allowedUserEnv))
	if allowed == "" {
		return false
	}
	return stripDevicePart(jid) == normalizePhoneToJID(allowed)
}

func normalizePhoneToJID(phone string) string {
	if strings.Contains(phone, "@") {
		return stripDevicePart(phone)
	}

	clean := ""
	for _, ch := range phone {
		if ch >= '0' && ch <= '9' {
			clean += string(ch)
		}
	}

	if strings.HasPrefix(clean, "0") {
		clean = "62" + clean[1:]
	} else if strings.HasPrefix(clean, "62") {
		// no change
	} else if clean != "" {
		clean = "62" + clean
	}

	return clean + "@s.whatsapp.net"
}

func sendScheduleResponse(ctx context.Context, chatID, message string) bool {
	queryType, ok := scheduleQueryType(message)
	if !ok {
		return false
	}

	loc := wibLoc()
	now := time.Now().In(loc)
	var start, end time.Time
	var title string
	var timeLayout string
	var limit int

	switch queryType {
	case "today":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		end = start.Add(24 * time.Hour)
		title = "Jadwal hari ini:"
		timeLayout = "15:04"
		limit = 10
	default:
		start = now
		end = now.Add(7 * 24 * time.Hour)
		title = "Jadwal terdekat:"
		timeLayout = "02 Jan 15:04"
		limit = 5
	}

	entries, err := database.GetEntriesByTriggerRange(start, end)
	if err != nil {
		log.Printf("[PITI-WA] Error ambil jadwal: %v", err)
		_ = Send(ctx, chatID, "Maaf, saya tidak bisa mengambil jadwal sekarang. Coba lagi ya.")
		return true
	}

	items := collectScheduleItems(entries, start, end)
	if len(items) == 0 {
		if queryType == "today" {
			_ = Send(ctx, chatID, "Belum ada agenda terjadwal hari ini.")
			return true
		}
		_ = Send(ctx, chatID, "Belum ada jadwal terdekat.")
		return true
	}

	if len(items) > limit {
		items = items[:limit]
	}

	lines := []string{title}
	for i, item := range items {
		when := item.SendAt.In(loc).Format(timeLayout)
		line := fmt.Sprintf("%d) %s WIB - %s", i+1, when, item.Content)
		if item.Targets != "" {
			line += " (Target: " + item.Targets + ")"
		}
		lines = append(lines, line)
	}

	_ = Send(ctx, chatID, strings.Join(lines, "\n"))
	return true
}

func scheduleQueryType(message string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(message))
	keywords := []string{
		"jadwal", "agenda", "schedule", "kegiatan", "acara",
		"terjadwal", "yang sudah dibuat", "yang sudah anda buat",
		"sudah dibuat", "sudah dibuatkan", "sudah dijadwalkan",
	}
	match := false
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			match = true
			break
		}
	}
	if !match {
		return "", false
	}
	if strings.Contains(lower, "hari ini") || strings.Contains(lower, "hariini") || strings.Contains(lower, "today") {
		return "today", true
	}
	return "upcoming", true
}

type scheduleItem struct {
	SendAt  time.Time
	Content string
	Targets string
}

func collectScheduleItems(entries []*models.Entry, start, end time.Time) []scheduleItem {
	items := []scheduleItem{}
	for _, entry := range entries {
		targets := uniqueTargetNames(entry.Metadata.Targets)
		for _, t := range entry.Metadata.Triggers {
			if t.Status != models.StatusPending {
				continue
			}
			if (t.SendAt.Equal(start) || t.SendAt.After(start)) && t.SendAt.Before(end) {
				items = append(items, scheduleItem{
					SendAt:  t.SendAt,
					Content: strings.TrimSpace(entry.Content),
					Targets: targets,
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].SendAt.Before(items[j].SendAt)
	})

	return items
}

func uniqueTargetNames(targets []models.Target) string {
	seen := map[string]struct{}{}
	names := []string{}
	for _, t := range targets {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			name = strings.TrimSpace(t.ID)
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func autocorrectKeywords(message string) string {
	// Autocorrect ringan untuk kata kunci agar intent tidak salah deteksi.
	replacements := map[string]string{
		"buatkamn":   "buatkan",
		"buatakan":   "buatkan",
		"bautkan":    "buatkan",
		"buatkna":    "buatkan",
		"ingatakn":   "ingatkan",
		"ingtkan":    "ingatkan",
		"ingtk":      "ingatkan",
		"kirimakn":   "kirimkan",
		"kiriman":    "kirimkan",
		"krimkan":    "kirimkan",
		"jadwl":      "jadwal",
		"pengingatn": "pengingat",
	}

	fields := strings.Fields(message)
	changed := false
	for i, token := range fields {
		trimmed := strings.Trim(token, ",.!?;:")
		lower := strings.ToLower(trimmed)
		if repl, ok := replacements[lower]; ok {
			fields[i] = strings.Replace(token, trimmed, repl, 1)
			changed = true
		}
	}

	if !changed {
		return message
	}
	return strings.Join(fields, " ")
}

// applyConversationalFeedback menerapkan koreksi natural ke entry yang ada
func applyConversationalFeedback(ctx context.Context, from, chatID, feedback string, existing *models.Entry) {
	updated, err := gemini.ParseFeedbackAuto(ctx, feedback, existing)
	if err != nil {
		log.Printf("[PITI-WA] Error parsing feedback: %v", err)
		_ = Send(ctx, chatID, "⚠️ PITI: Maaf, gagal memahami koreksimu. Coba lebih spesifik ya!")
		return
	}

	// Reset semua trigger ke pending
	for i := range updated.Metadata.Triggers {
		updated.Metadata.Triggers[i].Status = models.StatusPending
		updated.Metadata.Triggers[i].SentAt = nil
	}

	if err := database.UpdateEntryMetadata(existing.ID, updated.Metadata); err != nil {
		_ = Send(ctx, chatID, "⚠️ PITI: Gagal menyimpan perubahan.")
		return
	}

	// Update content jika berubah
	if updated.Content != existing.Content {
		database.DB.Exec(`UPDATE entries SET content = $1 WHERE id = $2`, updated.Content, existing.ID)
	}

	result, _ := database.GetEntryByID(existing.ID)
	_ = Send(ctx, chatID, buildUpdateConfirmation(existing.ID, result))
	log.Printf("[PITI-WA] ✓ Entry #%d diupdate via percakapan", existing.ID)
}

// processNewEntry membuat entry baru dari pesan
func processNewEntry(ctx context.Context, from, chatID, message string) {
	parsed, err := gemini.Parse(ctx, message, from)
	if err != nil {
		log.Printf("[PITI-WA] Error parsing: %v", err)
		_ = Send(ctx, chatID, "⚠️ PITI: Maaf, gagal memproses pesanmu. Coba lagi ya!")
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
		log.Printf("[PITI-WA] Error save: %v", err)
		_ = Send(ctx, chatID, "⚠️ PITI: Gagal menyimpan. Coba lagi!")
		return
	}

	_ = Send(ctx, chatID, buildConfirmationMessage(id, parsed))
	log.Printf("[PITI-WA] ✓ Entry #%d dibuat dari pesan WA", id)
}

// processFeedbackByID menangani format "#8 ubah..." (tetap didukung)
func processFeedbackByID(ctx context.Context, from, chatID, message string) {
	var entryID int
	var feedbackText string
	if !parseIDAndFeedback(message, &entryID, &feedbackText) || entryID == 0 {
		_ = Send(ctx, chatID, "⚠️ Format: #<ID> <koreksi>\nContoh: #8 ubah jam ke 15:00")
		return
	}

	existing, err := database.GetEntryByID(entryID)
	if err != nil {
		_ = Send(ctx, chatID, "⚠️ PITI: Entry tidak ditemukan.")
		return
	}

	applyConversationalFeedback(ctx, from, chatID, feedbackText, existing)
}

// buildUpdateConfirmation membangun pesan konfirmasi setelah update
func buildUpdateConfirmation(id int, entry *models.Entry) string {
	wib := wibLoc()
	msg := "✅ *PITI* - Berhasil diperbarui!\n\n"
	msg += "📌 *ID:* #" + itoa(id) + "\n"
	if entry != nil {
		msg += "💬 *Pesan:* " + entry.Content + "\n"
		if len(entry.Metadata.Triggers) > 0 {
			msg += "⏰ *Jadwal baru:*\n"
			for _, t := range entry.Metadata.Triggers {
				msg += "  • " + t.SendAt.In(wib).Format("02 Jan 2006 15:04 WIB") + "\n"
			}
		}
	}
	return msg
}

// buildConfirmationMessage membangun pesan konfirmasi entry baru
func buildConfirmationMessage(id int, parsed *models.ParsedEntry) string {
	wib := wibLoc()
	typeEmoji := map[models.EntryType]string{
		models.EntryTypeReminder:        "⏰",
		models.EntryTypeAnnouncement:    "📢",
		models.EntryTypePersonalMessage: "💬",
		models.EntryTypeTopic:           "📝",
	}
	emoji := typeEmoji[parsed.Type]
	if emoji == "" {
		emoji = "📋"
	}

	msg := emoji + " *PITI* - Perintah diterima!\n\n"
	msg += "📌 *ID:* #" + itoa(id) + "\n"
	msg += "📋 *Tipe:* " + string(parsed.Type) + "\n"
	msg += "💬 *Pesan:* " + parsed.Content + "\n"

	if len(parsed.Metadata.Targets) > 0 {
		msg += "👥 *Target:*\n"
		for _, t := range parsed.Metadata.Targets {
			msg += "  • " + t.Name + "\n"
		}
	}

	if len(parsed.Metadata.Triggers) > 0 {
		msg += "⏰ *Jadwal kirim:*\n"
		for _, t := range parsed.Metadata.Triggers {
			msg += "  • " + t.SendAt.In(wib).Format("02 Jan 2006 15:04 WIB") + "\n"
		}
	}

	msg += "\n_Untuk koreksi: cukup ketik \"ubah jamnya jadi..\" atau \"ingatkan jam..\"_"
	return msg
}

// wibLoc mengembalikan timezone WIB
func wibLoc() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*60*60)
	}
	return loc
}

// parseIDAndFeedback mem-parse format "#12 feedback text"
func parseIDAndFeedback(message string, entryID *int, feedback *string) bool {
	msg := strings.TrimPrefix(message, "#")
	parts := strings.SplitN(msg, " ", 2)
	if len(parts) < 2 {
		return false
	}
	id := 0
	for _, ch := range parts[0] {
		if ch < '0' || ch > '9' {
			return false
		}
		id = id*10 + int(ch-'0')
	}
	*entryID = id
	*feedback = strings.TrimSpace(parts[1])
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
