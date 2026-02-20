package queue

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type extractProgress struct{ pct float64; file string }

// Status values for downloads.
const (
	StatusQueued      = "queued"
	StatusDownloading = "downloading"
	StatusProcessing  = "processing"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
)

// Download represents a download item.
type Download struct {
	ID              string
	Name            string
	Category        string
	Status          string
	TotalBytes      int64
	DownloadedBytes int64
	TotalSegments   int
	DoneSegments    int
	Path            string
	NZBData         []byte
	CreatedAt       time.Time
	CompletedAt     *time.Time
	ErrorMsg        string
	Speed           float64 // bytes per second (live, not persisted)
	ExtractPct      float64 // 0–100 during StatusProcessing (in-memory, not persisted)
	ExtractFile     string  // basename currently being extracted (in-memory, not persisted)
}

// Progress returns the download progress as a percentage.
func (d *Download) Progress() float64 {
	if d.TotalSegments == 0 {
		return 0
	}
	return float64(d.DoneSegments) / float64(d.TotalSegments) * 100
}

// Manager manages the download queue backed by SQLite.
type Manager struct {
	db           *sql.DB
	mu           sync.RWMutex
	paused       bool
	extractMu    sync.RWMutex
	extractState map[string]extractProgress
}

// NewManager creates a new queue manager with a SQLite database.
func NewManager(dbPath string) (*Manager, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	m := &Manager{db: db, extractState: make(map[string]extractProgress)}
	if err := m.initDB(); err != nil {
		return nil, fmt.Errorf("initializing database: %w", err)
	}
	return m, nil
}

func (m *Manager) initDB() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS downloads (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'queued',
			total_bytes INTEGER DEFAULT 0,
			downloaded_bytes INTEGER DEFAULT 0,
			total_segments INTEGER DEFAULT 0,
			done_segments INTEGER DEFAULT 0,
			path TEXT DEFAULT '',
			nzb_data BLOB,
			error_msg TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);
	`)
	return err
}

// Add adds a new download to the queue.
func (m *Manager) Add(dl *Download) error {
	_, err := m.db.Exec(`
		INSERT INTO downloads (id, name, category, status, total_bytes, total_segments, nzb_data, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		dl.ID, dl.Name, dl.Category, StatusQueued,
		dl.TotalBytes, dl.TotalSegments, dl.NZBData, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("inserting download: %w", err)
	}
	log.Printf("Added download: %s (%s)", dl.Name, dl.ID)
	return nil
}

// Get retrieves a download by ID.
func (m *Manager) Get(id string) (*Download, error) {
	dl := &Download{}
	var completedAt sql.NullTime
	err := m.db.QueryRow(`
		SELECT id, name, category, status, total_bytes, downloaded_bytes,
			   total_segments, done_segments, path, nzb_data, error_msg,
			   created_at, completed_at
		FROM downloads WHERE id = ?`, id).Scan(
		&dl.ID, &dl.Name, &dl.Category, &dl.Status,
		&dl.TotalBytes, &dl.DownloadedBytes,
		&dl.TotalSegments, &dl.DoneSegments,
		&dl.Path, &dl.NZBData, &dl.ErrorMsg,
		&dl.CreatedAt, &completedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("querying download %s: %w", id, err)
	}
	if completedAt.Valid {
		dl.CompletedAt = &completedAt.Time
	}
	return dl, nil
}

// UpdateProgress updates the download progress.
func (m *Manager) UpdateProgress(id string, downloadedBytes int64, doneSegments int) error {
	_, err := m.db.Exec(`
		UPDATE downloads SET downloaded_bytes = ?, done_segments = ?
		WHERE id = ?`, downloadedBytes, doneSegments, id)
	return err
}

// UpdateStatus updates the status of a download.
func (m *Manager) UpdateStatus(id, status string) error {
	if status == StatusCompleted || status == StatusFailed {
		_, err := m.db.Exec(`
			UPDATE downloads SET status = ?, completed_at = ?
			WHERE id = ?`, status, time.Now(), id)
		return err
	}
	_, err := m.db.Exec(`UPDATE downloads SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdatePath updates the storage path for a download.
func (m *Manager) UpdatePath(id, path string) error {
	_, err := m.db.Exec(`UPDATE downloads SET path = ? WHERE id = ?`, path, id)
	return err
}

// SetError marks a download as failed with an error message.
func (m *Manager) SetError(id, errMsg string) error {
	_, err := m.db.Exec(`
		UPDATE downloads SET status = ?, error_msg = ?, completed_at = ?
		WHERE id = ?`, StatusFailed, errMsg, time.Now(), id)
	return err
}

// SetExtractProgress updates the in-memory extraction progress for a download.
func (m *Manager) SetExtractProgress(id string, pct float64, file string) {
	m.extractMu.Lock()
	defer m.extractMu.Unlock()
	m.extractState[id] = extractProgress{pct: pct, file: file}
}

// ClearExtractProgress removes the extraction progress entry for a download.
func (m *Manager) ClearExtractProgress(id string) {
	m.extractMu.Lock()
	defer m.extractMu.Unlock()
	delete(m.extractState, id)
}

func (m *Manager) getExtractProgress(id string) (float64, string, bool) {
	m.extractMu.RLock()
	defer m.extractMu.RUnlock()
	ep, ok := m.extractState[id]
	return ep.pct, ep.file, ok
}

// GetQueue returns all active (non-completed) downloads.
func (m *Manager) GetQueue() ([]*Download, error) {
	rows, err := m.db.Query(`
		SELECT id, name, category, status, total_bytes, downloaded_bytes,
			   total_segments, done_segments, path, error_msg, created_at
		FROM downloads
		WHERE status IN (?, ?, ?)
		ORDER BY created_at ASC`,
		StatusQueued, StatusDownloading, StatusProcessing,
	)
	if err != nil {
		return nil, fmt.Errorf("querying queue: %w", err)
	}
	defer rows.Close()

	var result []*Download
	for rows.Next() {
		dl := &Download{}
		err := rows.Scan(
			&dl.ID, &dl.Name, &dl.Category, &dl.Status,
			&dl.TotalBytes, &dl.DownloadedBytes,
			&dl.TotalSegments, &dl.DoneSegments,
			&dl.Path, &dl.ErrorMsg, &dl.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning queue row: %w", err)
		}
		result = append(result, dl)
	}
	for _, dl := range result {
		if pct, file, ok := m.getExtractProgress(dl.ID); ok {
			dl.ExtractPct = pct
			dl.ExtractFile = file
		}
	}
	return result, nil
}

// GetHistory returns completed and failed downloads.
func (m *Manager) GetHistory() ([]*Download, error) {
	rows, err := m.db.Query(`
		SELECT id, name, category, status, total_bytes, downloaded_bytes,
			   total_segments, done_segments, path, error_msg,
			   created_at, completed_at
		FROM downloads
		WHERE status IN (?, ?)
		ORDER BY completed_at DESC`,
		StatusCompleted, StatusFailed,
	)
	if err != nil {
		return nil, fmt.Errorf("querying history: %w", err)
	}
	defer rows.Close()

	var result []*Download
	for rows.Next() {
		dl := &Download{}
		var completedAt sql.NullTime
		err := rows.Scan(
			&dl.ID, &dl.Name, &dl.Category, &dl.Status,
			&dl.TotalBytes, &dl.DownloadedBytes,
			&dl.TotalSegments, &dl.DoneSegments,
			&dl.Path, &dl.ErrorMsg,
			&dl.CreatedAt, &completedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning history row: %w", err)
		}
		if completedAt.Valid {
			dl.CompletedAt = &completedAt.Time
		}
		result = append(result, dl)
	}
	return result, nil
}

// GetNextQueued returns the next queued download (FIFO).
func (m *Manager) GetNextQueued() (*Download, error) {
	dl := &Download{}
	err := m.db.QueryRow(`
		SELECT id, name, category, status, total_bytes, downloaded_bytes,
			   total_segments, done_segments, path, nzb_data, error_msg, created_at
		FROM downloads
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1`, StatusQueued).Scan(
		&dl.ID, &dl.Name, &dl.Category, &dl.Status,
		&dl.TotalBytes, &dl.DownloadedBytes,
		&dl.TotalSegments, &dl.DoneSegments,
		&dl.Path, &dl.NZBData, &dl.ErrorMsg, &dl.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying next queued: %w", err)
	}
	return dl, nil
}

// IsPaused returns whether the queue is paused.
func (m *Manager) IsPaused() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.paused
}

// SetPaused sets the paused state.
func (m *Manager) SetPaused(paused bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = paused
	if paused {
		log.Println("Download queue PAUSED")
	} else {
		log.Println("Download queue RESUMED")
	}
}

// Close closes the database connection.
func (m *Manager) Close() error {
	return m.db.Close()
}
