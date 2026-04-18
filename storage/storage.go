package storage

import (
	"database/sql"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Storage struct {
	db *sql.DB
}

func sanitizeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

func DBPath(host, username, inbox string) string {
	filename := fmt.Sprintf("sent_contacts__%s__%s__%s.db", sanitizeFileName(host), sanitizeFileName(username), sanitizeFileName(inbox))
	return filepath.Join("storage", filename)
}

// SentDBPath returns the sent-contacts DB path for a host/user (per-account)
// This creates a single whitelist DB shared across inboxes for the same account.
func SentDBPath(host, username string) string {
	filename := fmt.Sprintf("sent_contacts__%s__%s.db", sanitizeFileName(host), sanitizeFileName(username))
	return filepath.Join("storage", filename)
}

func New(dbPath string) (*Storage, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure sqlite journaling: %w", err)
	}

	if _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS contacts (
            email TEXT PRIMARY KEY,
            last_seen_at DATETIME NOT NULL
        );
    `); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create contacts table: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create metadata table: %w", err)
	}

	return &Storage{db: db}, nil
}

func (s *Storage) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Storage) HasContact(email string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("storage is not initialized")
	}

	normalized, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}

	row := s.db.QueryRow(`SELECT 1 FROM contacts WHERE email = ? LIMIT 1`, normalized)
	var exists int
	if err := row.Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Storage) ContactCount() (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("storage is not initialized")
	}

	row := s.db.QueryRow(`SELECT COUNT(*) FROM contacts`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Storage) BatchAddContacts(emails []string, seenAt time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	if len(emails) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(`
        INSERT INTO contacts(email, last_seen_at)
        VALUES (?, ?)
        ON CONFLICT(email) DO UPDATE SET last_seen_at = MAX(last_seen_at, excluded.last_seen_at)
    `)
	if err != nil {
		return err
	}
	defer stmt.Close()

	seenAtUTC := seenAt.UTC().Format(time.RFC3339)
	added := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		normalized, err := normalizeEmail(email)
		if err != nil || normalized == "" {
			continue
		}
		if _, ok := added[normalized]; ok {
			continue
		}
		added[normalized] = struct{}{}
		if _, err := stmt.Exec(normalized, seenAtUTC); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// GetMailbox returns a persisted mailbox name for a given role (e.g. "spam", "sent").
// Returns empty string and nil error when no mapping exists.
func (s *Storage) GetMailbox(role string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("storage is not initialized")
	}
	row := s.db.QueryRow(`SELECT value FROM metadata WHERE key = ? LIMIT 1`, role)
	var value string
	if err := row.Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// SetMailbox persists a mailbox name for a given role (e.g. "spam", "sent").
func (s *Storage) SetMailbox(role, mailbox string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	_, err := s.db.Exec(`
		INSERT INTO metadata(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, role, mailbox)
	return err
}

func (s *Storage) PruneOlderThan(cutoff time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	_, err := s.db.Exec(`DELETE FROM contacts WHERE last_seen_at < ?`, cutoff.UTC().Format(time.RFC3339))
	return err
}

// MergeContactsFromFile imports contacts from a legacy SQLite DB into the
// currently opened sent-contact storage. Existing contacts are merged by email.
func (s *Storage) MergeContactsFromFile(sourcePath string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("storage is not initialized")
	}

	if strings.TrimSpace(sourcePath) == "" {
		return 0, nil
	}
	if _, err := os.Stat(sourcePath); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	srcDB, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open source sqlite database: %w", err)
	}
	defer srcDB.Close()

	rows, err := srcDB.Query(`SELECT email, last_seen_at FROM contacts`)
	if err != nil {
		return 0, fmt.Errorf("failed to read contacts from source database: %w", err)
	}
	defer rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(`
        INSERT INTO contacts(email, last_seen_at)
        VALUES (?, ?)
        ON CONFLICT(email) DO UPDATE SET last_seen_at = MAX(last_seen_at, excluded.last_seen_at)
    `)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	imported := 0
	for rows.Next() {
		var email string
		var lastSeen sql.NullString
		if err := rows.Scan(&email, &lastSeen); err != nil {
			return 0, err
		}

		normalized, err := normalizeEmail(email)
		if err != nil || normalized == "" {
			continue
		}

		seenAt := strings.TrimSpace(lastSeen.String)
		if seenAt == "" {
			seenAt = time.Now().UTC().Format(time.RFC3339)
		}

		if _, err := stmt.Exec(normalized, seenAt); err != nil {
			return 0, err
		}
		imported++
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return imported, nil
}

func normalizeEmail(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("empty email address")
	}

	parsed, err := mail.ParseAddress(address)
	if err != nil {
		// Fallback: if the value already looks like a bare email, use it directly.
		if strings.Contains(address, "@") {
			return strings.ToLower(strings.TrimSpace(address)), nil
		}
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(parsed.Address)), nil
}

func ParseAddressList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	list, err := mail.ParseAddressList(value)
	if err != nil {
		// If the header is malformed, attempt a best-effort split by commas.
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			normalized, err := normalizeEmail(part)
			if err == nil && normalized != "" {
				out = append(out, normalized)
			}
		}
		return out
	}

	out := make([]string, 0, len(list))
	for _, addr := range list {
		if addr.Address != "" {
			out = append(out, strings.ToLower(strings.TrimSpace(addr.Address)))
		}
	}
	return out
}
