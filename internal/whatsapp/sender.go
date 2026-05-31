package whatsapp

import (
	"context"
	"log"
)

// Sender adalah interface untuk mengirim pesan WA
// Ini memudahkan testing (mock) dan nanti bisa diganti dengan implementasi nyata
type Sender interface {
	Send(ctx context.Context, targetID, message string) error
}

// defaultSender adalah implementasi yang digunakan saat ini
var defaultSender Sender = &MockSender{}

// SetSender mengganti sender (digunakan saat WA sudah terintegrasi)
func SetSender(s Sender) {
	defaultSender = s
}

// Send mengirim pesan ke target menggunakan sender yang aktif
func Send(ctx context.Context, targetID, message string) error {
	return defaultSender.Send(ctx, targetID, message)
}

// ---
// MockSender: digunakan saat WA belum terintegrasi
// Hanya log ke console
// ---

type MockSender struct{}

func (m *MockSender) Send(ctx context.Context, targetID, message string) error {
	log.Printf("[WA-MOCK] ─────────────────────────────────")
	log.Printf("[WA-MOCK] Kirim ke  : %s", targetID)
	log.Printf("[WA-MOCK] Pesan     : %s", message)
	log.Printf("[WA-MOCK] ─────────────────────────────────")
	return nil
}
