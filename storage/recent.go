package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultRecentSnippetBytes = 250

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
	_, err := s.db.Exec(`INSERT INTO consolidations(summary, created_at) VALUES (?, ?)`, summary, time.Now().UTC().Format(time.RFC3339))
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

	messages, err := s.GetRecentMessages(limit, time.Now().UTC().Add(-since))
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
