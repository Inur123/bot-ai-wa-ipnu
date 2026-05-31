# AI Agent Flexible Messaging System

## 1. Deskripsi Sistem
Sistem ini adalah AI-powered agent otonom yang dapat menangani semua jenis komunikasi dan automasi pesan, termasuk:
- Reminder (rapat, tugas, event)
- Pengumuman ke grup
- Pesan pribadi / personal message
- Topik diskusi atau catatan internal

**Fitur utama:**
- Multi-target (grup + beberapa chat pribadi)
- Multi-trigger (satu pesan bisa dikirim beberapa kali pada waktu berbeda)
- Feedback / reschedule otomatis
- History / referensi: AI bisa mengambil data entry sebelumnya
- Backend Golang untuk scheduler & logika utama
- Parsing natural language via Gemini API
- Database tunggal untuk semua entry → minimalis dan fleksibel

---

## 2. Arsitektur Sistem

```
User/Admin (chat pribadi WA)
        │
        ▼
WA Bot Listener
        │
        ▼
Hermes Agent Backend (Golang)
        │
        ├─> Gemini API (parsing natural language)
        │
        └─> Database tunggal "entries"
                  │
                  └─> Scheduler backend → kirim pesan ke target (grup/pribadi)
                          │
                          └─> Feedback / reschedule → update entry → kirim ulang
```

---

## 3. Database

### Nama database: `entries`

### Struktur Tabel: `entries`
```sql
CREATE TABLE entries (
    id SERIAL PRIMARY KEY,
    type TEXT,               -- "reminder", "announcement", "personal_message", "topic", dll
    content TEXT,            -- isi pesan / topik / catatan
    metadata JSONB,          -- {targets, triggers, tags, user_id, notes}
    status TEXT DEFAULT 'pending', -- pending, sent, corrected, done
    created_at TIMESTAMP DEFAULT now()
);
```

---

## 4. Contoh Prompt / Perintah WA

### 4.1 Reminder Multi-Trigger
```
@Bot
Buat reminder rapat:
Waktu rapat: besok jam 10:00
Target: Grup Marketing + +628123456789
Pesan: Halo, {name}! Rapat akan dimulai jam 10.
Trigger:
- 1 jam sebelum
- 30 menit sebelum
```

### 4.2 Pengumuman Grup
```
@Bot
Kirim pengumuman:
Waktu: hari ini jam 15:00
Target: Grup Engineering
Pesan: Mohon hadir di meeting online jam 15:00
Trigger:
- 10 menit sebelum
```

### 4.3 Pengumuman Pribadi
```
@Bot
Kirim pesan pribadi:
Target: +628987654321 + +6287654321
Pesan: Halo, {name}! Harap baca pengumuman terbaru.
```

### 4.4 Reschedule / Koreksi
```
@Bot
Update reminder:
Reminder ID: 12
Waktu baru: besok jam 11:00
Catatan: Maaf tanggal salah sebelumnya.
```

### 4.5 Feedback di grup
- User membalas pesan yang sudah terkirim:  
  > “Maaf, reminder salah jam, ubah ke jam 14:00.”  
- AI parsing → update database → scheduler kirim ulang sesuai koreksi.

---

## 5. Backend Golang Pseudocode

```go
func ListenWA() {
    for {
        msg := ReceiveMessageFromWA()
        parsed := GeminiParse(msg.Text)
        SaveToDB(parsed) // simpan ke tabel entries
    }
}

func SchedulerLoop() {
    for {
        now := time.Now()
        entries := DBGetPendingTriggers(now) // ambil trigger yang waktunya tiba
        for _, e := range entries {
            for _, t := range e.Metadata.Targets {
                msg := personalizeMessage(e.Content, t.Name)
                SendMessageToWA(t.ID, msg)
            }
            UpdateTriggerStatus(e.ID, "sent")
        }
        time.Sleep(30 * time.Second)
    }
}

func HandleFeedback() {
    for {
        feedback := ReceiveMessageFromWA()
        parsed := GeminiParse(feedback.Text)
        ApplyUpdateToDB(parsed) // update entry/triggers
    }
}
```

---

## 6. Kelebihan Sistem

1. Database tunggal → minimal, fleksibel, menyimpan semua jenis data.
2. AI fleksibel / full power → bisa memproses natural language bebas, memahami pesan dan resep pengiriman.
3. Multi-target / multi-trigger → reminder atau pengumuman bisa ke grup + beberapa chat pribadi, dengan waktu berbeda-beda.
4. Feedback loop otomatis → user bisa membalas pesan yang salah → AI update dan kirim ulang.
5. History / referensi → AI bisa mengambil data sebelumnya untuk konteks topik, personalisasi, atau menghindari duplikasi.

---

Sistem ini siap diimplementasikan dengan backend Golang, Hermes Agent + Gemini API, dan WhatsApp Bot integration, menggunakan satu database tunggal bernama `entries`.
