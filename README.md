# Hermes Agent - AI WhatsApp Messaging Bot

Bot AI untuk automasi pesan WhatsApp IPNU berbasis Golang + Gemini API.

## Arsitektur

```
User/Admin (chat WA)  ──► WA Bot Listener
                                │
                                ▼
                    Hermes Agent Backend (Go)
                         │           │
                    Gemini API    PostgreSQL
                                      │
                              Scheduler Loop
                                      │
                              Kirim pesan WA
```

## Struktur Direktori

```
bot-ai-wa-ipnu/
├── cmd/
│   └── server/
│       └── main.go          # Entry point
├── internal/
│   ├── models/
│   │   └── entry.go         # Data model
│   ├── database/
│   │   ├── db.go            # Koneksi & migrasi
│   │   └── repository.go    # CRUD queries
│   ├── gemini/
│   │   └── parser.go        # Parsing natural language
│   ├── scheduler/
│   │   └── scheduler.go     # Scheduler loop
│   ├── handler/
│   │   └── handler.go       # HTTP handlers
│   └── whatsapp/
│       └── sender.go        # WA abstraction (mock saat ini)
├── .env.example
├── go.mod
└── README.md
```

## Setup

### 1. Copy .env dan isi konfigurasi

```bash
cp .env.example .env
```

Edit `.env`:
```env
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=yourpassword
DB_NAME=hermes_agent

GEMINI_API_KEY=your_key_here

APP_PORT=8080
SCHEDULER_INTERVAL_SECONDS=30
```

### 2. Buat database PostgreSQL

```sql
CREATE DATABASE hermes_agent;
```

Tabel dibuat otomatis saat server start (auto-migrate).

### 3. Build & Run

```bash
go build -o hermes ./cmd/server
./hermes
```

Atau langsung:
```bash
go run ./cmd/server
```

## REST API

### Buat entry baru

```bash
POST /api/entries
{
  "raw_message": "Buat reminder rapat besok jam 10:00, kirim ke Grup Marketing, pesan: Halo {name}! Rapat jam 10. Kirim 1 jam sebelum dan 30 menit sebelum.",
  "user_id": "628123456789"
}
```

### Lihat semua entry

```bash
GET /api/entries?limit=20&offset=0
```

### Lihat satu entry

```bash
GET /api/entries/1
```

### Koreksi / reschedule

```bash
POST /api/entries/1/feedback
{
  "feedback": "Ubah waktu rapat ke jam 14:00, bukan jam 10:00",
  "user_id": "628123456789"
}
```

### Batalkan entry

```bash
DELETE /api/entries/1
```

### Simulasi pesan WA (testing tanpa nomor WA)

```bash
POST /api/simulate
{
  "from": "628123456789",
  "message": "Kirim pengumuman ke Grup PC IPNU: Rapat malam ini jam 20:00 wajib hadir"
}
```

## Status Entry

| Status      | Keterangan                              |
|-------------|----------------------------------------|
| `pending`   | Menunggu dikirim                        |
| `sent`      | Sudah dikirim                          |
| `corrected` | Dikoreksi oleh user                    |
| `done`      | Semua trigger selesai                  |
| `cancelled` | Dibatalkan                             |

## Integrasi WhatsApp (TODO)

Saat ini WA menggunakan **MockSender** yang hanya log ke console.

Nanti setelah punya nomor WA, implementasi di `internal/whatsapp/sender.go`:

```go
// Contoh menggunakan whatsmeow
type WhatsmeowSender struct {
    client *whatsmeow.Client
}

func (s *WhatsmeowSender) Send(ctx context.Context, targetID, message string) error {
    jid, _ := types.ParseJID(targetID)
    _, err := s.client.SendMessage(ctx, jid, &waProto.Message{
        Conversation: proto.String(message),
    })
    return err
}
```

Lalu di `main.go`:
```go
whatsapp.SetSender(&whatsapp.WhatsmeowSender{client: waClient})
```
