package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
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
	dbPath := os.Getenv("WA_DB_PATH")
	if dbPath == "" {
		dbPath = "./piti_wa.db"
	}

	// Setup SQLite store untuk menyimpan sesi WA
	dbLog := waLog.Stdout("WA-DB", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), dbLog)
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
				mentioned = isSelfMentioned(v.Message.GetExtendedTextMessage().GetContextInfo().GetMentionedJID())
			}
			if !mentioned {
				lower := strings.ToLower(text)
				if strings.Contains(lower, "@piti") || (strings.Contains(lower, "@") && strings.Contains(lower, "piti")) {
					mentioned = true
				} else if hasNumericTag(text) {
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

func isSelfMentioned(mentioned []string) bool {
	if WAClient == nil || WAClient.Store == nil || WAClient.Store.ID == nil {
		return false
	}
	self := stripDevicePart(WAClient.Store.ID.String())
	for _, jid := range mentioned {
		if stripDevicePart(jid) == self {
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

func hasNumericTag(text string) bool {
	for _, token := range strings.Fields(text) {
		if strings.HasPrefix(token, "@") && len(token) > 2 {
			allDigits := true
			for _, ch := range token[1:] {
				if ch < '0' || ch > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
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
