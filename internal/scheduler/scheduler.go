package scheduler

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"bot-ai-wa-ipnu/internal/database"
	"bot-ai-wa-ipnu/internal/models"
	"bot-ai-wa-ipnu/internal/timeutil"
	"bot-ai-wa-ipnu/internal/whatsapp"
)

// Run menjalankan scheduler loop secara blocking (jalankan di goroutine)
func Run(ctx context.Context) {
	// Mulai sinkronisasi berkala data Laci Pelajar NU
	go StartLaciSync(ctx)

	intervalSec, err := strconv.Atoi(os.Getenv("SCHEDULER_INTERVAL_SECONDS"))
	if err != nil || intervalSec <= 0 {
		intervalSec = 30
	}

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	log.Printf("[Scheduler] Berjalan dengan interval %d detik", intervalSec)

	for {
		select {
		case <-ctx.Done():
			log.Println("[Scheduler] Berhenti")
			return
		case t := <-ticker.C:
			processEntries(ctx, t)
		}
	}
}

// processEntries memproses semua entry yang trigger-nya sudah tiba waktunya
func processEntries(ctx context.Context, now time.Time) {
	entries, err := database.GetPendingTriggers(now)
	if err != nil {
		log.Printf("[Scheduler] Error mengambil pending triggers: %v", err)
		return
	}

	if len(entries) == 0 {
		return
	}

	log.Printf("[Scheduler] Memproses %d entry pada %s", len(entries), now.Format("15:04:05"))

	for _, entry := range entries {
		go processEntry(ctx, entry, now)
	}
}

// processEntry memproses satu entry - kirim pesan ke semua target yang trigger-nya sudah tiba
func processEntry(ctx context.Context, entry *models.Entry, now time.Time) {
	for _, trigger := range entry.Metadata.Triggers {
		// Skip trigger yang bukan pending atau belum waktunya
		if trigger.Status != models.StatusPending || trigger.SendAt.After(now) {
			continue
		}

		log.Printf("[Scheduler] Entry #%d - trigger %s - kirim ke %d target",
			entry.ID, trigger.ID, len(entry.Metadata.Targets))

		sendSuccess := true

		for _, target := range entry.Metadata.Targets {
			targetJID, resolveErr := resolveTargetJID(ctx, target)
			if resolveErr != nil {
				log.Printf("[Scheduler] GAGAL resolve target %s (%s): %v", target.Name, target.ID, resolveErr)
				sendSuccess = false
				continue
			}
			if targetJID == "" {
				log.Printf("[Scheduler] GAGAL resolve target %s (%s): JID kosong", target.Name, target.ID)
				sendSuccess = false
				continue
			}
			msg := buildOutgoingMessage(entry, target.Name, target.Type)

			// Gunakan hard timeout dengan goroutine agar tidak terpengaruh jika library whatsmeow hang
			errChan := make(chan error, 1)
			go func() {
				// Gunakan context background untuk goroutine independen agar tidak di-cancel saat timeout terjadi
				errChan <- whatsapp.Send(context.Background(), targetJID, msg)
			}()

			var err error
			select {
			case err = <-errChan:
				// Selesai
			case <-time.After(15 * time.Second):
				err = fmt.Errorf("timeout (koneksi WhatsApp tidak merespon dalam 15 detik)")
			}

			if err != nil {
				log.Printf("[Scheduler] GAGAL kirim ke %s (%s): %v", target.Name, targetJID, err)
				sendSuccess = false
			} else {
				log.Printf("[Scheduler] ✓ Terkirim ke %s (%s)", target.Name, targetJID)
			}

			// Tambahkan delay acak 3-5 detik antar target agar pengiriman tidak sekaligus (anti-spam)
			time.Sleep(time.Duration(3+rand.Intn(3)) * time.Second)
		}

		// Update status trigger
		status := models.StatusSent
		if !sendSuccess {
			// Jika sebagian gagal, tetap mark sent tapi log error
			// Bisa dikembangkan: retry logic
			status = models.StatusSent
		}

		if err := database.UpdateTriggerStatus(entry.ID, trigger.ID, status); err != nil {
			log.Printf("[Scheduler] Error update trigger status: %v", err)
		}
	}
}

// buildOutgoingMessage membangun pesan reminder/pengumuman yang dikirim ke target
// Format profesional seperti template WA reminder
func buildOutgoingMessage(entry *models.Entry, targetName, targetType string) string {
	if targetName == "" {
		targetName = "Anda"
	}

	// Ganti {name} di content
	content := personalizeMessage(entry.Content, targetName)
	trimmed := strings.TrimSpace(content)

	// Jika content sudah berformat agenda, tetap pakai header/footer template dan gunakan isi apa adanya.
	if looksLikeStructuredAgenda(trimmed) {
		return buildStructuredAgendaMessage(entry.Type, targetName, targetType, trimmed)
	}

	// Jika content memiliki baris baru (\n), artinya user menyertakan template format sendiri.
	// Kirim pesan tersebut langsung (setelah dipersonalisasi) tanpa membungkusnya ke template default.
	if strings.Contains(content, "\n") {
		return content
	}

	// Pengumuman tanpa format: kirim konten apa adanya + footer.
	if entry.Type == models.EntryTypeAnnouncement {
		return buildPlainAnnouncementMessage(content)
	}

	// Pilih header sesuai tipe untuk pesan single-line
	var header string
	switch entry.Type {
	case models.EntryTypeReminder:
		header = "⏰ *[REMINDER AGENDA]*"
	case models.EntryTypeAnnouncement:
		header = "📢 *[PENGUMUMAN]*"
	case models.EntryTypePersonalMessage:
		header = "💬 *[PESAN PRIBADI]*"
	default:
		header = "📋 *[INFORMASI]*"
	}

	// Bangun baris detail untuk template standar
	msg := header + "\n\n"
	msg += buildGreetingLine(entry.Type, targetName, targetType)
	msg += "🗒 *Kegiatan :* " + content + "\n"

	// Tambah tanggal & waktu jika ada event_at
	if entry.Metadata.EventAt != nil {
		msg += "📅 *Tanggal  :* " + entry.Metadata.EventAt.In(timeutil.Location()).Format("Monday, 02 January 2006") + "\n"
		msg += "⏰ *Waktu    :* " + entry.Metadata.EventAt.In(timeutil.Location()).Format("15:04") + " WIB\n"
	}

	// Tambah catatan jika ada
	if entry.Metadata.Notes != "" {
		msg += "📌 *Catatan  :* " + entry.Metadata.Notes + "\n"
	}

	msg += "\n"
	msg += "🤖 _Pesan ini dikirim otomatis oleh BOT, tidak perlu membalas pesan ini._\n"
	msg += "\n"
	msg += "*PITI AI*\n"
	msg += "_Pelajar NU Magetan_"

	return msg
}

func buildPlainAnnouncementMessage(content string) string {
	msg := "📢 *[PENGUMUMAN]*\n\n"
	msg += content + "\n\n"
	msg += "🤖 _Pesan ini dikirim otomatis oleh BOT, tidak perlu membalas pesan ini._\n\n"
	msg += "*PITI AI*\n"
	msg += "_Pelajar NU Magetan_"
	return msg
}

func buildStructuredAgendaMessage(entryType models.EntryType, targetName, targetType, content string) string {
	var header string
	switch entryType {
	case models.EntryTypeReminder:
		header = "⏰ *[REMINDER AGENDA]*"
	case models.EntryTypeAnnouncement:
		header = ""
	case models.EntryTypePersonalMessage:
		header = "💬 *[PESAN PRIBADI]*"
	default:
		header = "📋 *[INFORMASI]*"
	}

	msg := ""
	if header != "" {
		msg = header + "\n\n"
		msg += buildGreetingLine(entryType, targetName, targetType)
	}
	msg += content + "\n\n"
	msg += "🤖 _Pesan ini dikirim otomatis oleh BOT, tidak perlu membalas pesan ini._\n\n"
	msg += "*PITI AI*\n"
	msg += "_Pelajar NU Magetan_"
	return msg
}

func buildGreetingLine(entryType models.EntryType, targetName, targetType string) string {
	if entryType == models.EntryTypeAnnouncement {
		if targetType == "group" {
			return "Halo, berikut pengumuman:\n\n"
		}
		return "Halo *" + targetName + "*, berikut pengumuman:\n\n"
	}

	if targetType == "group" {
		return "Halo, berikut pengingat agenda Anda:\n\n"
	}
	return "Halo *" + targetName + "*, berikut pengingat agenda Anda:\n\n"
}

func looksLikeStructuredAgenda(content string) bool {
	if content == "" {
		return false
	}
	keywords := []string{
		"kegiatan", "tanggal", "waktu", "lokasi", "catatan",
	}
	for _, kw := range keywords {
		if strings.Contains(strings.ToLower(content), kw+" :") || strings.Contains(strings.ToLower(content), kw+":") {
			return true
		}
	}
	// Emoji label umum di input user
	if strings.Contains(content, "📝") || strings.Contains(content, "📆") ||
		strings.Contains(content, "⏰") || strings.Contains(content, "📍") ||
		strings.Contains(content, "📌") {
		return true
	}
	return false
}

// personalizeMessage mengganti placeholder {name} dengan nama target
func personalizeMessage(content, name string) string {
	if name == "" {
		name = "Anda"
	}
	result := content
	result = strings.ReplaceAll(result, "{name}", name)
	result = strings.ReplaceAll(result, "{nama}", name)
	result = strings.ReplaceAll(result, "{{name}}", name)
	result = strings.ReplaceAll(result, "{{nama}}", name)
	return result
}

// normalizeJID mengkonversi nomor HP lokal ke format JID WhatsApp
// Contoh: 085790865350 → 6285790865350@s.whatsapp.net
//
//	+6285790865350 → 6285790865350@s.whatsapp.net
//	6285790865350 → 6285790865350@s.whatsapp.net
//	628xxx@s.whatsapp.net → tidak berubah (sudah benar)
func normalizeJID(phone string) string {
	// Sudah format JID lengkap
	if strings.Contains(phone, "@") {
		return phone
	}

	// Bersihkan karakter non-digit kecuali +
	clean := ""
	for _, ch := range phone {
		if ch >= '0' && ch <= '9' {
			clean += string(ch)
		}
	}

	// Konversi prefix
	if strings.HasPrefix(clean, "0") {
		// 085xxx → 6285xxx
		clean = "62" + clean[1:]
	} else if strings.HasPrefix(clean, "62") {
		// sudah 62xxx, tidak perlu ubah
	} else if len(clean) > 0 {
		// nomor lain, tambahkan 62
		clean = "62" + clean
	}

	return clean + "@s.whatsapp.net"
}

func resolveTargetJID(ctx context.Context, target models.Target) (string, error) {
	if strings.Contains(target.ID, "@") {
		return stripDevicePart(target.ID), nil
	}

	if target.Type == "group" {
		candidates := []string{}
		if strings.TrimSpace(target.ID) != "" {
			candidates = append(candidates, strings.TrimSpace(target.ID))
		}
		if strings.TrimSpace(target.Name) != "" {
			candidates = append(candidates, strings.TrimSpace(target.Name))
		}

		for _, name := range candidates {
			jid, err := whatsapp.ResolveGroupJIDByName(ctx, name)
			if err == nil {
				return jid, nil
			}
			// Coba tanpa prefix "grup" atau "group" jika ada.
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "grup ") {
				trimmed := strings.TrimSpace(name[5:])
				if trimmed != "" {
					jid, err = whatsapp.ResolveGroupJIDByName(ctx, trimmed)
					if err == nil {
						return jid, nil
					}
				}
			}
			if strings.HasPrefix(lower, "group ") {
				trimmed := strings.TrimSpace(name[6:])
				if trimmed != "" {
					jid, err = whatsapp.ResolveGroupJIDByName(ctx, trimmed)
					if err == nil {
						return jid, nil
					}
				}
			}
		}
		return "", fmt.Errorf("grup '%s' tidak ditemukan", strings.TrimSpace(target.Name))
	}

	return normalizeJID(target.ID), nil
}

// stripDevicePart menghapus device part pada JID user (contoh: 2218:7@lid -> 2218@lid).
func stripDevicePart(jid string) string {
	parts := strings.SplitN(jid, "@", 2)
	if len(parts) != 2 {
		return jid
	}
	user := parts[0]
	if colon := strings.Index(user, ":"); colon != -1 {
		user = user[:colon]
	}
	return user + "@" + parts[1]
}
