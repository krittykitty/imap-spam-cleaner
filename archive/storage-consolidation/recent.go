//go:build archive

package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultRecentSnippetBytes = 250

const (
	metadataInitialPopulationDone    = "initial_population_done"
	metadataConsolidationPending     = "consolidation_pending_count"
	metadataConsolidationLastRunTime = "consolidation_last_run"
)

var sanitizeFileName = func(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

func RecentDBPath(host, username, inbox string) string {
	filename := fmt.Sprintf("recent_messages__%s__%s__%s.db", sanitizeFileName(host), sanitizeFileName(username), sanitizeFileName(inbox))
	return filepath.Join("storage", filename)
}

type RecentStore struct {
	db *sql.DB
}

type RecentMessage struct {
	UID         uint32
	From        string
	To          string
	Subject     string
	Snippet     string
	SpamScore   *float64
	LLMReason   string
	Whitelisted bool
	Date        time.Time
}

type Consolidation struct {
	Summary   string
	CreatedAt time.Time
}

func NewRecent(dbPath string) (*RecentStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create recent store directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open recent sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping recent sqlite database: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure recent sqlite journaling: %w", err)
	}

	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure recent sqlite busy timeout: %w", err)
	}

	if _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS recent_messages (
            uid INTEGER PRIMARY KEY,
            from_address TEXT,
            to_address TEXT,
            subject TEXT,
            snippet TEXT,
            date DATETIME NOT NULL,
            spam_score REAL,
            llm_reason TEXT,
            whitelisted INTEGER NOT NULL DEFAULT 0,
            updated_at DATETIME NOT NULL
        );
    `); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create recent_messages table: %w", err)
	}

	if _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS consolidations (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            summary TEXT NOT NULL,
            created_at DATETIME NOT NULL
        );
    `); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create consolidations table: %w", err)
	}

	if _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS metadata (
            key TEXT PRIMARY KEY,
            value TEXT NOT NULL,
            updated_at DATETIME NOT NULL
        );
    `); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create metadata table: %w", err)
	}

	return &RecentStore{db: db}, nil
}

func (s *RecentStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *RecentStore) UpsertMessage(msg RecentMessage) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("recent store is not initialized")
	}
	if msg.UID == 0 {
		return fmt.Errorf("invalid message UID")
	}

	spamScore := sql.NullFloat64{}
	if msg.SpamScore != nil {
		spamScore.Float64 = *msg.SpamScore
		spamScore.Valid = true
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	date := msg.Date.UTC().Format(time.RFC3339)

	_, err := s.db.Exec(`
        INSERT INTO recent_messages (
            uid, from_address, to_address, subject, snippet, date, spam_score, llm_reason, whitelisted, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(uid) DO UPDATE SET
            from_address = excluded.from_address,
            to_address = excluded.to_address,
            subject = excluded.subject,
            snippet = excluded.snippet,
            date = excluded.date,
            spam_score = excluded.spam_score,
            llm_reason = excluded.llm_reason,
            whitelisted = excluded.whitelisted,
            updated_at = excluded.updated_at;
    `, msg.UID, msg.From, msg.To, msg.Subject, msg.Snippet, date, spamScore, msg.LLMReason, boolToInt(msg.Whitelisted), updatedAt)
	if err != nil {
		return err
	}
	return nil
}

func (s *RecentStore) GetLatestConsolidation() (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("recent store is not initialized")
	}

	row := s.db.QueryRow(`SELECT summary FROM consolidations ORDER BY created_at DESC LIMIT 1`)
	var summary string
	if err := row.Scan(&summary); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return summary, nil
}

func (s *RecentStore) GetLatestConsolidationMeta() (Consolidation, error) {
	if s == nil || s.db == nil {
		return Consolidation{}, fmt.Errorf("recent store is not initialized")
	}

	row := s.db.QueryRow(`SELECT summary, created_at FROM consolidations ORDER BY created_at DESC LIMIT 1`)
	var summary, createdAt string
	if err := row.Scan(&summary, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return Consolidation{}, nil
		}
		return Consolidation{}, err
	}

	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Consolidation{}, err
	}

	return Consolidation{Summary: summary, CreatedAt: created}, nil
}

func (s *RecentStore) SaveConsolidation(summary string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("recent store is not initialized")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`INSERT INTO consolidations(summary, created_at) VALUES (?, ?)`, summary, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
        INSERT INTO metadata (key, value, updated_at)
        VALUES (?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
    `, metadataConsolidationLastRunTime, now, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *RecentStore) IsInitialPopulationDone() (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("recent store is not initialized")
	}
	value, err := s.getMetadataValue(metadataInitialPopulationDone)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func (s *RecentStore) MarkInitialPopulationDone() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("recent store is not initialized")
	}
	return s.setMetadataValue(metadataInitialPopulationDone, "true")
}

func (s *RecentStore) GetConsolidationPendingCount() (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("recent store is not initialized")
	}

	value, err := s.getMetadataValue(metadataConsolidationPending)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid consolidation pending count %q: %w", value, err)
	}
	if n < 0 {
		return 0, nil
	}
	return n, nil
}

func (s *RecentStore) AddConsolidationPending(delta int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("recent store is not initialized")
	}

	current, err := s.GetConsolidationPendingCount()
	if err != nil {
		return 0, err
	}
	if delta <= 0 {
		return current, nil
	}

	next := current + delta
	if err := s.setMetadataValue(metadataConsolidationPending, strconv.Itoa(next)); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *RecentStore) ResetConsolidationPending() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("recent store is not initialized")
	}
	return s.setMetadataValue(metadataConsolidationPending, "0")
}

func (s *RecentStore) GetConsolidationLastRun() (time.Time, error) {
	if s == nil || s.db == nil {
		return time.Time{}, fmt.Errorf("recent store is not initialized")
	}

	value, err := s.getMetadataValue(metadataConsolidationLastRunTime)
	if err != nil {
		return time.Time{}, err
	}
	if value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid consolidation last run timestamp %q: %w", value, err)
		}
		return parsed, nil
	}

	meta, err := s.GetLatestConsolidationMeta()
	if err != nil {
		return time.Time{}, err
	}
	return meta.CreatedAt, nil
}

func (s *RecentStore) getMetadataValue(key string) (string, error) {
	row := s.db.QueryRow(`SELECT value FROM metadata WHERE key = ?`, key)
	var value string
	if err := row.Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func (s *RecentStore) setMetadataValue(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
        INSERT INTO metadata (key, value, updated_at)
        VALUES (?, ?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
    `, key, value, now)
	return err
}

func (s *RecentStore) GetRecentMessages(limit int, since time.Time) ([]RecentMessage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("recent store is not initialized")
	}

	query := `
        SELECT uid, from_address, to_address, subject, snippet, spam_score, llm_reason, whitelisted, date
        FROM recent_messages
        WHERE date >= ?
        ORDER BY date DESC
        LIMIT ?
    `

	cutoff := since.UTC().Format(time.RFC3339)
	rows, err := s.db.Query(query, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]RecentMessage, 0)
	for rows.Next() {
		var msg RecentMessage
		var spamScore sql.NullFloat64
		var whitelistedInt int
		var dateStr string
		if err := rows.Scan(&msg.UID, &msg.From, &msg.To, &msg.Subject, &msg.Snippet, &spamScore, &msg.LLMReason, &whitelistedInt, &dateStr); err != nil {
			return nil, err
		}
		if spamScore.Valid {
			msg.SpamScore = &spamScore.Float64
		}
		msg.Whitelisted = whitelistedInt != 0
		msg.Date, err = time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (s *RecentStore) GetConsolidatedContext(limit int, since time.Duration) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("recent store is not initialized")
	}

	summary, err := s.GetLatestConsolidation()
	if err != nil {
		return "", err
	}

	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().UTC().Add(-since)
	}
	messages, err := s.GetRecentMessages(limit, cutoff)
	if err != nil {
		return "", err
	}

	if summary == "" && len(messages) == 0 {
		return "", nil
	}

	var builder strings.Builder
	if summary != "" {
		builder.WriteString("Recent consolidated context:\n")
		builder.WriteString(summary)
		builder.WriteString("\n\n")
	}
	if len(messages) > 0 {
		builder.WriteString("Recent messages:\n")
		for _, msg := range messages {
			reason := msg.LLMReason
			if reason == "" {
				reason = "none"
			}
			builder.WriteString(fmt.Sprintf("- From: %s | To: %s | Subject: %s | Whitelisted: %t | Score: %s | Reason: %s\n", msg.From, msg.To, msg.Subject, msg.Whitelisted, scoreString(msg.SpamScore), sanitizeLine(reason)))
		}
	}

	return builder.String(), nil
}

func (s *RecentStore) PruneOlderThan(cutoff time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("recent store is not initialized")
	}
	_, err := s.db.Exec(`DELETE FROM recent_messages WHERE date < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM consolidations WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	return err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func scoreString(score *float64) string {
	if score == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", *score)
}

func sanitizeLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if len(value) > 200 {
		return value[:200] + "..."
	}
	return value
}
