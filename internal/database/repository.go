package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"bot-ai-wa-ipnu/internal/models"
)

// SaveEntry menyimpan entry baru ke database
func SaveEntry(e *models.Entry) (int, error) {
	metaJSON, err := json.Marshal(e.Metadata)
	if err != nil {
		return 0, fmt.Errorf("gagal marshal metadata: %w", err)
	}

	var id int
	query := `
		INSERT INTO entries (type, content, metadata, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`
	err = DB.QueryRow(query, string(e.Type), e.Content, metaJSON, string(e.Status)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("gagal insert entry: %w", err)
	}
	return id, nil
}

// GetEntryByID mengambil satu entry berdasarkan ID
func GetEntryByID(id int) (*models.Entry, error) {
	query := `SELECT id, type, content, metadata, status, created_at FROM entries WHERE id = $1`
	row := DB.QueryRow(query, id)
	return scanEntry(row)
}

// GetAllEntries mengambil semua entry (untuk REST API / admin)
func GetAllEntries(limit, offset int) ([]*models.Entry, error) {
	query := `
		SELECT id, type, content, metadata, status, created_at
		FROM entries
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := DB.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("gagal query entries: %w", err)
	}
	defer rows.Close()

	var entries []*models.Entry
	for rows.Next() {
		e, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GetPendingTriggers mengambil semua trigger yang sudah waktunya dikirim
func GetPendingTriggers(now time.Time) ([]*models.Entry, error) {
	// Ambil entries dengan status pending yang punya trigger yang send_at <= now
	query := `
		SELECT id, type, content, metadata, status, created_at
		FROM entries
		WHERE status = 'pending'
		AND metadata->'triggers' IS NOT NULL
	`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("gagal query pending triggers: %w", err)
	}
	defer rows.Close()

	var result []*models.Entry
	for rows.Next() {
		e, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		// Filter di aplikasi: cek apakah ada trigger yang send_at <= now dan masih pending
		for _, t := range e.Metadata.Triggers {
			if t.Status == models.StatusPending && !t.SendAt.After(now) {
				result = append(result, e)
				break
			}
		}
	}
	return result, rows.Err()
}

// GetEntriesByTriggerRange mengambil entry pending yang punya trigger dalam rentang waktu.
func GetEntriesByTriggerRange(start, end time.Time) ([]*models.Entry, error) {
	query := `
		SELECT id, type, content, metadata, status, created_at
		FROM entries
		WHERE status = 'pending'
		AND metadata->'triggers' IS NOT NULL
	`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("gagal query entries: %w", err)
	}
	defer rows.Close()

	var result []*models.Entry
	for rows.Next() {
		e, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		for _, t := range e.Metadata.Triggers {
			if t.Status != models.StatusPending {
				continue
			}
			if (t.SendAt.Equal(start) || t.SendAt.After(start)) && t.SendAt.Before(end) {
				result = append(result, e)
				break
			}
		}
	}
	return result, rows.Err()
}

// UpdateTriggerStatus memperbarui status trigger tertentu dalam entry
func UpdateTriggerStatus(entryID int, triggerID string, status models.EntryStatus) error {
	// Ambil entry dulu
	entry, err := GetEntryByID(entryID)
	if err != nil {
		return err
	}

	// Update trigger yang sesuai
	now := time.Now()
	updated := false
	for i, t := range entry.Metadata.Triggers {
		if t.ID == triggerID {
			entry.Metadata.Triggers[i].Status = status
			if status == models.StatusSent {
				entry.Metadata.Triggers[i].SentAt = &now
			}
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("trigger %s tidak ditemukan di entry %d", triggerID, entryID)
	}

	// Cek apakah semua trigger sudah selesai
	allDone := true
	for _, t := range entry.Metadata.Triggers {
		if t.Status == models.StatusPending {
			allDone = false
			break
		}
	}

	newStatus := entry.Status
	if allDone {
		newStatus = models.StatusDone
	}

	metaJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("gagal marshal metadata: %w", err)
	}

	query := `UPDATE entries SET metadata = $1, status = $2 WHERE id = $3`
	_, err = DB.Exec(query, metaJSON, string(newStatus), entryID)
	return err
}

// UpdateEntryStatus memperbarui status entry
func UpdateEntryStatus(entryID int, status models.EntryStatus) error {
	_, err := DB.Exec(`UPDATE entries SET status = $1 WHERE id = $2`, string(status), entryID)
	return err
}

// UpdateEntryMetadata memperbarui metadata entry (untuk reschedule/koreksi)
func UpdateEntryMetadata(entryID int, meta models.Metadata) error {
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("gagal marshal metadata: %w", err)
	}
	_, err = DB.Exec(
		`UPDATE entries SET metadata = $1, status = 'pending' WHERE id = $2`,
		metaJSON, entryID,
	)
	return err
}

// DeleteEntry menghapus entry (soft delete via status)
func DeleteEntry(entryID int) error {
	return UpdateEntryStatus(entryID, models.StatusCancelled)
}

// GetLastEntryByUser mengambil entry pending terakhir milik user tertentu
// Digunakan untuk koreksi percakapan tanpa perlu sebut #ID
func GetLastEntryByUser(userID string) (*models.Entry, error) {
	query := `
		SELECT id, type, content, metadata, status, created_at
		FROM entries
		WHERE status = 'pending'
		AND metadata->>'user_id' = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := DB.QueryRow(query, userID)
	entry, err := scanEntry(row)
	if err != nil {
		return nil, fmt.Errorf("tidak ada entry pending untuk user %s", userID)
	}
	return entry, nil
}

// --- helper ---

func scanEntry(row *sql.Row) (*models.Entry, error) {
	var e models.Entry
	var metaRaw []byte
	err := row.Scan(&e.ID, &e.Type, &e.Content, &metaRaw, &e.Status, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("gagal scan entry: %w", err)
	}
	if err := json.Unmarshal(metaRaw, &e.Metadata); err != nil {
		return nil, fmt.Errorf("gagal unmarshal metadata: %w", err)
	}
	return &e, nil
}

func scanEntryRow(rows *sql.Rows) (*models.Entry, error) {
	var e models.Entry
	var metaRaw []byte
	err := rows.Scan(&e.ID, &e.Type, &e.Content, &metaRaw, &e.Status, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("gagal scan entry row: %w", err)
	}
	if err := json.Unmarshal(metaRaw, &e.Metadata); err != nil {
		return nil, fmt.Errorf("gagal unmarshal metadata: %w", err)
	}
	return &e, nil
}
