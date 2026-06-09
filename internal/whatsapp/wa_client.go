package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/lib/pq"
	qrterminal "github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// WhatsmeowSender adalah implementasi nyata menggunakan library whatsmeow
type WhatsmeowSender struct {
	client *whatsmeow.Client
}

// Send mengirim pesan ke target WA ID
func (s *WhatsmeowSender) Send(ctx context.Context, targetID, message string) error {
	log.Printf("[WA-DEBUG] Send dipanggil untuk target: %s", targetID)
	jid, err := types.ParseJID(targetID)
	if err != nil {
		log.Printf("[WA-DEBUG] Gagal parse JID %s: %v", targetID, err)
		return fmt.Errorf("gagal parse JID '%s': %w", targetID, err)
	}

	log.Printf("[WA-DEBUG] Memanggil s.client.SendMessage untuk %s...", targetID)
	_, err = s.client.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(message),
	})
	if err != nil {
		log.Printf("[WA-DEBUG] SendMessage gagal untuk %s: %v", targetID, err)
		return fmt.Errorf("gagal kirim pesan ke %s: %w", targetID, err)
	}

	log.Printf("[WA] ✓ Pesan terkirim ke %s", targetID)
	return nil
}

// ResolveGroupJIDByName mencari JID grup berdasarkan nama grup di akun yang sedang login.
func ResolveGroupJIDByName(ctx context.Context, name string) (string, error) {
	if WAClient == nil {
		return "", fmt.Errorf("WA client belum diinisialisasi")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("nama grup kosong")
	}

	groups, err := WAClient.GetJoinedGroups(ctx)
	if err != nil {
		return "", fmt.Errorf("gagal ambil daftar grup: %w", err)
	}

	for _, g := range groups {
		if strings.EqualFold(strings.TrimSpace(g.Name), trimmed) {
			return g.JID.String(), nil
		}
	}

	return "", fmt.Errorf("grup '%s' tidak ditemukan", trimmed)
}

// WAClient adalah instance global WhatsApp client
var WAClient *whatsmeow.Client

// InitWhatsApp menginisialisasi koneksi WhatsApp via whatsmeow
// Mengembalikan channel yang akan menerima pesan masuk
func InitWhatsApp(ctx context.Context) (<-chan *IncomingMessage, error) {
	storeDriver := strings.TrimSpace(os.Getenv("WA_STORE_DRIVER"))
	if storeDriver == "" {
		storeDriver = "postgres"
	}
	storeDSN := strings.TrimSpace(os.Getenv("WA_STORE_DSN"))
	if storeDSN == "" {
		storeDSN = buildWADSN()
	}

	// Setup store untuk menyimpan sesi WA
	dbLog := waLog.Stdout("WA-DB", "WARN", true)
	container, err := sqlstore.New(ctx, storeDriver, storeDSN, dbLog)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat WA store: %w", err)
	}

	// Ambil device dari store (sesi tersimpan)
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gagal mendapatkan device: %w", err)
	}

	clientLog := waLog.Stdout("PITI-WA", "INFO", true)
	WAClient = whatsmeow.NewClient(deviceStore, clientLog)

	// Channel untuk pesan masuk
	msgChan := make(chan *IncomingMessage, 100)

	// Event handler: terima pesan masuk
	WAClient.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			// Hanya proses pesan teks
			if v.Message.GetConversation() == "" && v.Message.GetExtendedTextMessage() == nil {
				return
			}
			text := v.Message.GetConversation()
			if text == "" {
				text = v.Message.GetExtendedTextMessage().GetText()
			}

			mentioned := false
			if v.Message.GetExtendedTextMessage() != nil && v.Message.GetExtendedTextMessage().GetContextInfo() != nil {
				ctxInfo := v.Message.GetExtendedTextMessage().GetContextInfo()
				mentioned = isSelfMentioned(ctxInfo.GetMentionedJID()) || isQuotedSelf(ctxInfo)
			}
			if !mentioned {
				lower := strings.ToLower(text)
				prefixLower := strings.ToLower(os.Getenv("WA_TRIGGER_PREFIX"))
				if prefixLower == "" {
					prefixLower = "@rimita"
				}
				// Cek jika mengandung prefix trigger (seperti @rimita) atau jika nomor bot di-tag secara numerik
				if strings.Contains(lower, prefixLower) {
					mentioned = true
				} else if isSelfNumericTag(text) {
					mentioned = true
				}
			}

			msgChan <- &IncomingMessage{
				From:      v.Info.Sender.String(),
				ChatID:    v.Info.Chat.String(),
				Message:   text,
				IsGroup:   v.Info.IsGroup,
				Mentioned: mentioned,
			}
		}
	})

	// Cek jika belum login, lakukan QR login
	if WAClient.Store.ID == nil {
		log.Println("[PITI-WA] Belum login. Memulai QR code login...")
		qrChan, _ := WAClient.GetQRChannel(ctx)
		if err := WAClient.Connect(); err != nil {
			return nil, fmt.Errorf("gagal connect WA: %w", err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				// Print QR code ke terminal (gunakan terminal QR renderer)
				printQRCode(evt.Code)
				log.Println("[PITI-WA] Scan QR code di atas dengan WhatsApp kamu!")
			} else if evt.Event == "success" {
				log.Println("[PITI-WA] ✅ Login WhatsApp berhasil!")
				break
			} else if evt.Event == "timeout" {
				return nil, fmt.Errorf("QR code timeout, coba lagi")
			}
		}
	} else {
		// Sudah ada sesi, langsung connect
		if err := WAClient.Connect(); err != nil {
			return nil, fmt.Errorf("gagal connect WA: %w", err)
		}
		log.Printf("[PITI-WA] ✅ Terhubung sebagai %s", WAClient.Store.ID)
	}

	// Aktifkan sender nyata
	SetSender(&WhatsmeowSender{client: WAClient})
	log.Println("[PITI-WA] WhatsApp sender aktif!")

	return msgChan, nil
}

func buildWADSN() string {
	host := getEnv("WA_DB_HOST", getEnv("DB_HOST", "localhost"))
	port := getEnv("WA_DB_PORT", getEnv("DB_PORT", "5432"))
	user := getEnv("WA_DB_USER", getEnv("DB_USER", "postgres"))
	password := getEnv("WA_DB_PASSWORD", getEnv("DB_PASSWORD", ""))
	name := getEnv("WA_DB_NAME", getEnv("DB_NAME", "hermes_agent"))
	sslmode := getEnv("WA_DB_SSLMODE", "disable")

	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", host, port, user, password, name, sslmode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Disconnect memutuskan koneksi WA dengan bersih
func Disconnect() {
	if WAClient != nil {
		WAClient.Disconnect()
		log.Println("[PITI-WA] Koneksi WhatsApp diputus")
	}
}

// IncomingMessage adalah pesan masuk dari WA
type IncomingMessage struct {
	From      string // JID pengirim
	ChatID    string // JID chat (grup atau personal)
	Message   string // Isi pesan
	IsGroup   bool   // Apakah dari grup
	Mentioned bool   // Apakah bot di-mention di grup
}

func isQuotedSelf(ctxInfo *waProto.ContextInfo) bool {
	if ctxInfo == nil || ctxInfo.Participant == nil {
		return false
	}
	quotedSender := stripDevicePart(*ctxInfo.Participant)
	if WAClient == nil || WAClient.Store == nil || WAClient.Store.ID == nil {
		return false
	}
	selfPhone := stripDevicePart(WAClient.Store.ID.String())
	var selfLID string
	if WAClient.Store.LID.User != "" {
		selfLID = stripDevicePart(WAClient.Store.LID.String())
	}
	return quotedSender == selfPhone || (selfLID != "" && quotedSender == selfLID)
}

func isSelfMentioned(mentioned []string) bool {
	if WAClient == nil || WAClient.Store == nil || WAClient.Store.ID == nil {
		return false
	}
	selfPhone := stripDevicePart(WAClient.Store.ID.String())
	var selfLID string
	if WAClient.Store.LID.User != "" {
		selfLID = stripDevicePart(WAClient.Store.LID.String())
	}
	for _, jid := range mentioned {
		stripped := stripDevicePart(jid)
		if stripped == selfPhone || (selfLID != "" && stripped == selfLID) {
			return true
		}
	}
	return false
}

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

func isSelfNumericTag(text string) bool {
	if WAClient == nil || WAClient.Store == nil || WAClient.Store.ID == nil {
		return false
	}
	selfPhone := WAClient.Store.ID.User
	var selfLIDPhone string
	if WAClient.Store.LID.User != "" {
		selfLIDPhone = WAClient.Store.LID.User
	}
	for _, token := range strings.Fields(text) {
		if strings.HasPrefix(token, "@") && len(token) > 2 {
			// Bersihkan karakter non-digit di belakang jika ada tanda baca
			tagNum := ""
			for _, ch := range token[1:] {
				if ch >= '0' && ch <= '9' {
					tagNum += string(ch)
				} else {
					break
				}
			}
			if tagNum == selfPhone || (selfLIDPhone != "" && tagNum == selfLIDPhone) {
				return true
			}
		}
	}
	return false
}

// printQRCode menampilkan QR code yang bisa di-scan langsung di terminal
func printQRCode(code string) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║      PITI - Scan QR Code ini         ║")
	fmt.Println("║  Buka WhatsApp > Perangkat Tertaut   ║")
	fmt.Println("║     > Tautkan Perangkat > Scan       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()
	qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
	fmt.Println()
	log.Println("[PITI-WA] ⏳ Menunggu scan QR... (QR akan refresh tiap 20 detik)")
}
