package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// EntryType mendefinisikan jenis entry
type EntryType string

const (
	EntryTypeReminder        EntryType = "reminder"
	EntryTypeAnnouncement    EntryType = "announcement"
	EntryTypePersonalMessage EntryType = "personal_message"
	EntryTypeTopic           EntryType = "topic"
)

// EntryStatus mendefinisikan status entry
type EntryStatus string

const (
	StatusPending   EntryStatus = "pending"
	StatusSent      EntryStatus = "sent"
	StatusCorrected EntryStatus = "corrected"
	StatusDone      EntryStatus = "done"
	StatusCancelled EntryStatus = "cancelled"
)

// Target adalah tujuan pengiriman pesan (grup atau pribadi)
type Target struct {
	ID   string `json:"id"`   // WA ID, e.g. "628123456789@s.whatsapp.net" atau "GROUP_ID@g.us"
	Name string `json:"name"` // Nama untuk personalisasi pesan
	Type string `json:"type"` // "group" atau "personal"
}

// Trigger adalah waktu pengiriman pesan
type Trigger struct {
	ID         string      `json:"id"`
	SendAt     time.Time   `json:"send_at"`              // Waktu pasti pengiriman
	OffsetMin  int         `json:"offset_min,omitempty"` // Menit sebelum event (opsional)
	Status     EntryStatus `json:"status"`               // pending, sent, dll
	SentAt     *time.Time  `json:"sent_at,omitempty"`
}

// Metadata adalah data tambahan yang disimpan sebagai JSONB
type Metadata struct {
	Targets  []Target  `json:"targets"`
	Triggers []Trigger `json:"triggers"`
	Tags     []string  `json:"tags,omitempty"`
	UserID   string    `json:"user_id,omitempty"`  // WA ID pengirim perintah
	Notes    string    `json:"notes,omitempty"`
	EventAt  *time.Time `json:"event_at,omitempty"` // Waktu event utama (opsional)
}

// Value implements driver.Valuer untuk JSONB PostgreSQL
func (m Metadata) Value() (driver.Value, error) {
	return json.Marshal(m)
}

// Scan implements sql.Scanner untuk JSONB PostgreSQL
func (m *Metadata) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to unmarshal JSONB: expected []byte, got %T", value)
	}
	return json.Unmarshal(b, m)
}

// Entry adalah entitas utama dalam database
type Entry struct {
	ID        int         `db:"id"         json:"id"`
	Type      EntryType   `db:"type"        json:"type"`
	Content   string      `db:"content"     json:"content"`
	Metadata  Metadata    `db:"metadata"    json:"metadata"`
	Status    EntryStatus `db:"status"      json:"status"`
	CreatedAt time.Time   `db:"created_at"  json:"created_at"`
}

// CreateEntryRequest adalah request untuk membuat entry baru via REST API
type CreateEntryRequest struct {
	RawMessage string `json:"raw_message"` // Pesan natural language dari user
	UserID     string `json:"user_id"`     // WA ID pengirim
}

// ParsedEntry adalah hasil parsing Gemini API
type ParsedEntry struct {
	Type     EntryType  `json:"type"`
	Content  string     `json:"content"`
	Metadata Metadata   `json:"metadata"`
}
