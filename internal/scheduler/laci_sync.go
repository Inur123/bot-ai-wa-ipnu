package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type LaciDataResponse struct {
	Success         bool              `json:"success"`
	Timestamp       string            `json:"timestamp"`
	ArsipSurat      []ArsipSurat      `json:"arsipSurat"`
	Anggota         []Anggota         `json:"anggota"`
	PengajuanBerkas []PengajuanBerkas `json:"pengajuanBerkas"`
	BerkasPimpinan  []BerkasPimpinan  `json:"berkasPimpinan"`
	BerkasSP        []BerkasSP        `json:"berkasSP"`
	AgendaKegiatan  []AgendaKegiatan  `json:"agendaKegiatan"`
	Periode         []Periode         `json:"periode"`
	Users           []User            `json:"users"`
}

type ArsipSurat struct {
	ID               string  `json:"id"`
	NoSurat          string  `json:"noSurat"`
	PengirimPenerima string  `json:"pengirimPenerima"`
	Perihal          string  `json:"perihal"`
	Deskripsi        *string `json:"deskripsi"`
	JenisSurat       string  `json:"jenisSurat"`
	Organisasi       string  `json:"organisasi"`
	Tanggal          string  `json:"tanggal"`
	File             *string `json:"file"`
	Uploader         string  `json:"uploader"`
	Periode          string  `json:"periode"`
}

type Anggota struct {
	ID                string  `json:"id"`
	Nik               *string `json:"nik"`
	Nama              string  `json:"nama"`
	Email             *string `json:"email"`
	NoHp              *string `json:"noHp"`
	TempatLahir       *string `json:"tempatLahir"`
	TanggalLahir      *string `json:"tanggalLahir"`
	Alamat            *string `json:"alamat"`
	Pekerjaan         *string `json:"pekerjaan"`
	JenjangPendidikan *string `json:"jenjangPendidikan"`
	Uploader          string  `json:"uploader"`
	Periode           string  `json:"periode"`
}

type PengajuanBerkas struct {
	ID           string  `json:"id"`
	NoSurat      *string `json:"noSurat"`
	Keperluan    string  `json:"keperluan"`
	Deskripsi    *string `json:"deskripsi"`
	Status       string  `json:"status"`
	File         string  `json:"file"`
	PacName      string  `json:"pacName"`
	PeriodePac   string  `json:"periodePac"`
	CatatanAdmin *string `json:"catatanAdmin"`
}

type BerkasPimpinan struct {
	ID       string  `json:"id"`
	Nama     string  `json:"nama"`
	Catatan  *string `json:"catatan"`
	Tanggal  string  `json:"tanggal"`
	File     string  `json:"file"`
	Uploader string  `json:"uploader"`
	Periode  string  `json:"periode"`
}

type BerkasSP struct {
	ID              string  `json:"id"`
	Nama            string  `json:"nama"`
	Catatan         *string `json:"catatan"`
	TanggalMulai    string  `json:"tanggalMulai"`
	TanggalBerakhir string  `json:"tanggalBerakhir"`
	File            *string `json:"file"`
	Uploader        string  `json:"uploader"`
	Periode         string  `json:"periode"`
}

type AgendaKegiatan struct {
	ID             string  `json:"id"`
	Judul          string  `json:"judul"`
	Deskripsi      *string `json:"deskripsi"`
	TanggalMulai   string  `json:"tanggalMulai"`
	TanggalSelesai *string `json:"tanggalSelesai"`
	Lokasi         *string `json:"lokasi"`
	Warna          string  `json:"warna"`
	Uploader       string  `json:"uploader"`
}

type Periode struct {
	ID        string `json:"id"`
	Nama      string `json:"nama"`
	IsActive  bool   `json:"isActive"`
	CreatedAt string `json:"createdAt"`
}

type User struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	IsActive      bool   `json:"isActive"`
	EmailVerified bool   `json:"emailVerified"`
}

// StartLaciSync mulai men-sync data Laci secara berkala di background
func StartLaciSync(ctx context.Context) {
	// Lakukan sync pertama kali saat startup
	syncLaci(ctx)

	// Sync ulang setiap 5 menit
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Laci-Sync] Dihentikan")
			return
		case <-ticker.C:
			syncLaci(ctx)
		}
	}
}

func syncLaci(ctx context.Context) {
	apiKey := os.Getenv("LACI_API_KEY")
	apiURL := os.Getenv("LACI_API_URL")

	if apiKey == "" || apiURL == "" {
		log.Println("[Laci-Sync] Warning: LACI_API_KEY atau LACI_API_URL kosong di env. Sinkronisasi dibatalkan.")
		return
	}

	log.Printf("[Laci-Sync] Memulai sinkronisasi data dari %s...", apiURL)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		log.Printf("[Laci-Sync] Gagal membuat request: %v", err)
		return
	}
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Laci-Sync] Gagal melakukan request HTTP: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Laci-Sync] Respons API tidak sukses: %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Laci-Sync] Gagal membaca body respons: %v", err)
		return
	}

	var data LaciDataResponse
	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("[Laci-Sync] Gagal unmarshal JSON: %v", err)
		return
	}

	// Buat folder knowledge jika belum ada
	dir := "knowledge"
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[Laci-Sync] Gagal membuat direktori knowledge: %v", err)
		return
	}

	// 1. Tulis File Ringkasan/Summary (Ukurannya kecil, tidak akan dipotong chunk oleh AI)
	summaryText := formatLaciSummary(data)
	summaryPath := filepath.Join(dir, "data_laci_summary.txt")
	if err := os.WriteFile(summaryPath, []byte(summaryText), 0644); err != nil {
		log.Printf("[Laci-Sync] Gagal menulis file data_laci_summary.txt: %v", err)
		return
	}

	// 2. Tulis File Detail Lengkap (Untuk pencarian query spesifik, misal mencari no surat tertentu)
	detailsText := formatLaciDetails(data)
	detailsPath := filepath.Join(dir, "data_laci_details.txt")
	if err := os.WriteFile(detailsPath, []byte(detailsText), 0644); err != nil {
		log.Printf("[Laci-Sync] Gagal menulis file data_laci_details.txt: %v", err)
		return
	}

	log.Printf("[Laci-Sync] Sukses mensinkronkan data Laci. (Summary & Details ditulis)")
}

// formatLaciSummary membuat ringkasan statistik total data agar AI bisa menjawab jumlah total data tanpa kendala chunking.
func formatLaciSummary(data LaciDataResponse) string {
	type UploaderSuratStats struct {
		Total  int
		Masuk  int
		Keluar int
	}

	// Hitung jumlah surat per uploader secara detail
	suratPerUploader := make(map[string]*UploaderSuratStats)
	suratMasuk := 0
	suratKeluar := 0
	for _, s := range data.ArsipSurat {
		if _, ok := suratPerUploader[s.Uploader]; !ok {
			suratPerUploader[s.Uploader] = &UploaderSuratStats{}
		}
		stats := suratPerUploader[s.Uploader]
		stats.Total++
		if s.JenisSurat == "MASUK" {
			stats.Masuk++
			suratMasuk++
		} else if s.JenisSurat == "KELUAR" {
			stats.Keluar++
			suratKeluar++
		}
	}

	// Hitung jumlah anggota per uploader/PAC
	anggotaPerUploader := make(map[string]int)
	for _, a := range data.Anggota {
		anggotaPerUploader[a.Uploader]++
	}

	var sb string
	sb += "=== STATISTIK & RINGKASAN DATA LACI PELAJAR NU KABUPATEN MAGETAN ===\n"
	sb += fmt.Sprintf("Terakhir Sinkronisasi: %s (WIB)\n\n", time.Now().Format("2006-01-02 15:04:05"))

	sb += "--- STATISTIK GLOBAL ---\n"
	sb += fmt.Sprintf("- Total Seluruh Arsip Surat di DB (Semua Periode): %d surat (Masuk: %d, Keluar: %d)\n", len(data.ArsipSurat), suratMasuk, suratKeluar)
	sb += fmt.Sprintf("- Total Seluruh Anggota Terdaftar: %d orang\n", len(data.Anggota))
	sb += fmt.Sprintf("- Total Pengajuan Berkas dari PAC ke Cabang: %d pengajuan\n", len(data.PengajuanBerkas))
	sb += fmt.Sprintf("- Total Berkas Pimpinan: %d berkas\n", len(data.BerkasPimpinan))
	sb += fmt.Sprintf("- Total Berkas SK/SP Cabang: %d berkas SK\n", len(data.BerkasSP))
	sb += fmt.Sprintf("- Total Agenda/Kegiatan Terdaftar: %d kegiatan\n", len(data.AgendaKegiatan))
	sb += fmt.Sprintf("- Total Periode Kepengurusan: %d periode\n", len(data.Periode))
	sb += fmt.Sprintf("- Total Akun Pengurus/PAC: %d akun\n\n", len(data.Users))

	sb += "--- DETAIL TOTAL ARSIP SURAT BERDASARKAN UPLOADER/PENGURUS ---\n"
	sb += "(Gunakan data ini untuk menjawab statistik surat milik uploader tertentu)\n"
	for name, stats := range suratPerUploader {
		sb += fmt.Sprintf("- %s: %d surat (Surat Masuk: %d, Surat Keluar: %d)\n", name, stats.Total, stats.Masuk, stats.Keluar)
	}
	sb += "\n"

	sb += "--- TOTAL DATA ANGGOTA BERDASARKAN PAC/PENGURUS ---\n"
	for name, count := range anggotaPerUploader {
		sb += fmt.Sprintf("- %s: %d anggota\n", name, count)
	}
	sb += "\n"

	// Tampilkan 5 Kegiatan Terdekat/Terbaru
	sb += "--- 5 AGENDA KEGIATAN TERBARU/TERDEKAT ---\n"
	limitKegiatan := len(data.AgendaKegiatan)
	if limitKegiatan > 5 {
		limitKegiatan = 5
	}
	for i := 0; i < limitKegiatan; i++ {
		k := data.AgendaKegiatan[i]
		tglMulai, _ := time.Parse(time.RFC3339, k.TanggalMulai)
		loc := "-"
		if k.Lokasi != nil {
			loc = *k.Lokasi
		}
		sb += fmt.Sprintf("- %s (Tanggal: %s, Lokasi: %s)\n", k.Judul, tglMulai.Format("02 Jan 2006"), loc)
	}
	sb += "\n"

	// Tampilkan 5 Surat Terbaru
	sb += "--- 5 ARSIP SURAT TERBARU ---\n"
	limitSurat := len(data.ArsipSurat)
	if limitSurat > 5 {
		limitSurat = 5
	}
	for i := 0; i < limitSurat; i++ {
		s := data.ArsipSurat[i]
		tgl, _ := time.Parse(time.RFC3339, s.Tanggal)
		sb += fmt.Sprintf("- No: %s | Hal: %s (%s, Org: %s, Tgl: %s, Oleh: %s)\n", s.NoSurat, s.Perihal, s.JenisSurat, s.Organisasi, tgl.Format("02 Jan 2006"), s.Uploader)
	}
	
	return sb
}

func formatLaciDetails(data LaciDataResponse) string {
	var sb string
	sb += "=== DETAIL DATA LENGKAP LACI PELAJAR NU KABUPATEN MAGETAN ===\n\n"

	// 1. Data Periode
	sb += "--- DAFTAR PERIODE MASAS KHIDMAT ---\n"
	for _, p := range data.Periode {
		status := "Non-aktif"
		if p.IsActive {
			status = "AKTIF"
		}
		sb += fmt.Sprintf("- Periode/Masa Khidmat: %s (Status: %s, ID: %s)\n", p.Nama, status, p.ID)
	}
	sb += "\n"

	// 2. Data Users / Akun PAC
	sb += "--- DAFTAR AKUN PENGURUS & PAC IPNU-IPPNU KABUPATEN MAGETAN ---\n"
	for _, u := range data.Users {
		status := "Non-aktif"
		if u.IsActive {
			status = "Aktif"
		}
		verified := "Belum Verifikasi"
		if u.EmailVerified {
			verified = "Terverifikasi"
		}
		sb += fmt.Sprintf("- Nama: %s (Email: %s, Jabatan/Role: %s, Status: %s, Verifikasi: %s)\n", u.Name, u.Email, u.Role, status, verified)
	}
	sb += "\n"

	// 3. Agenda Kegiatan
	sb += "--- DAFTAR KEGIATAN & AGENDA TERDAFTAR ---\n"
	for _, k := range data.AgendaKegiatan {
		tglMulai, err := time.Parse(time.RFC3339, k.TanggalMulai)
		tglMulaiStr := k.TanggalMulai
		if err == nil {
			tglMulaiStr = tglMulai.Format("02 January 2006 jam 15:04 WIB")
		}
		
		desc := "-"
		if k.Deskripsi != nil {
			desc = *k.Deskripsi
		}
		loc := "-"
		if k.Lokasi != nil {
			loc = *k.Lokasi
		}

		sb += fmt.Sprintf("- Agenda: %s\n  Waktu Mulai: %s\n  Lokasi: %s\n  Deskripsi: %s\n  Uploader: %s\n", k.Judul, tglMulaiStr, loc, desc, k.Uploader)
	}
	sb += "\n"

	// 4. Arsip Surat
	sb += "--- DAFTAR ARSIP SURAT (MASUK & KELUAR) ---\n"
	for _, s := range data.ArsipSurat {
		tgl, _ := time.Parse(time.RFC3339, s.Tanggal)
		tglStr := s.Tanggal
		if !tgl.IsZero() {
			tglStr = tgl.Format("02 January 2006")
		}
		desc := "-"
		if s.Deskripsi != nil {
			desc = *s.Deskripsi
		}
		sb += fmt.Sprintf("- Nomor Surat: %s\n  Perihal: %s\n  Jenis: %s (%s)\n  Tanggal Surat: %s\n  Pengirim/Penerima: %s\n  Deskripsi: %s\n  Uploader: %s (Periode: %s)\n", 
			s.NoSurat, s.Perihal, s.JenisSurat, s.Organisasi, tglStr, s.PengirimPenerima, desc, s.Uploader, s.Periode)
	}
	sb += "\n"

	// 5. Data Anggota
	sb += "--- DAFTAR ANGGOTA IPNU-IPPNU MAGETAN ---\n"
	for _, a := range data.Anggota {
		noHp := "-"
		if a.NoHp != nil {
			noHp = *a.NoHp
		}
		alamat := "-"
		if a.Alamat != nil {
			alamat = *a.Alamat
		}
		pendidikan := "-"
		if a.JenjangPendidikan != nil {
			pendidikan = *a.JenjangPendidikan
		}
		sb += fmt.Sprintf("- Nama: %s (Alamat: %s, No.HP: %s, Pendidikan: %s, Uploader: %s, Periode: %s)\n", 
			a.Nama, alamat, noHp, pendidikan, a.Uploader, a.Periode)
	}
	sb += "\n"

	// 6. Pengajuan Berkas PAC
	sb += "--- DAFTAR PENGAJUAN BERKAS DARI PAC KE CABANG (REKOMENDASI SK / LEGALITAS PAC) ---\n"
	for _, p := range data.PengajuanBerkas {
		noSurat := "-"
		if p.NoSurat != nil {
			noSurat = *p.NoSurat
		}
		catatan := "-"
		if p.CatatanAdmin != nil {
			catatan = *p.CatatanAdmin
		}
		sb += fmt.Sprintf("- Pengaju: %s (Keperluan: %s, Status: %s, No.Surat: %s, Catatan Admin: %s, Periode PAC: %s)\n", 
			p.PacName, p.Keperluan, p.Status, noSurat, catatan, p.PeriodePac)
	}
	sb += "\n"

	// 7. Berkas Pimpinan
	sb += "--- DAFTAR ARSIP BERKAS PIMPINAN ---\n"
	for _, b := range data.BerkasPimpinan {
		tgl, _ := time.Parse(time.RFC3339, b.Tanggal)
		tglStr := b.Tanggal
		if !tgl.IsZero() {
			tglStr = tgl.Format("02 January 2006")
		}
		catatan := "-"
		if b.Catatan != nil {
			catatan = *b.Catatan
		}
		sb += fmt.Sprintf("- Nama Berkas: %s\n  Tanggal: %s\n  Catatan: %s\n  Uploader: %s (Periode: %s)\n", 
			b.Nama, tglStr, catatan, b.Uploader, b.Periode)
	}
	sb += "\n"

	// 8. Berkas SP
	sb += "--- DAFTAR BERKAS SURAT KEPUTUSAN (SP) CABANG ---\n"
	for _, sp := range data.BerkasSP {
		catatan := "-"
		if sp.Catatan != nil {
			catatan = *sp.Catatan
		}
		sb += fmt.Sprintf("- Nama SK/SP: %s (Berlaku: %s s.d. %s, Catatan: %s, Uploader: %s, Periode: %s)\n", 
			sp.Nama, sp.TanggalMulai, sp.TanggalBerakhir, catatan, sp.Uploader, sp.Periode)
	}

	return sb
}
