package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

type FolderState struct {
	ID          string
	Name        string
	UIDValidity uint32
	NextUID     uint32
	DeltaLink   string
}

type MessageRecord struct {
	FolderID   string
	GraphID    string
	UID        uint32
	Subject    string
	IsRead     bool
	IsDraft    bool
	IsDeleted  bool
	ReceivedAt time.Time
	Size       int64
}

func NewStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS folders (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		uid_validity INTEGER NOT NULL,
		next_uid INTEGER NOT NULL DEFAULT 1,
		delta_link TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS messages (
		folder_id TEXT NOT NULL,
		graph_id TEXT NOT NULL,
		uid INTEGER NOT NULL,
		subject TEXT DEFAULT '',
		is_read INTEGER NOT NULL DEFAULT 0,
		is_draft INTEGER NOT NULL DEFAULT 0,
		is_deleted INTEGER NOT NULL DEFAULT 0,
		received_at INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (folder_id, graph_id),
		UNIQUE (folder_id, uid)
	);

	CREATE INDEX IF NOT EXISTS idx_messages_folder_uid ON messages(folder_id, uid);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) EnsureFolder(folderID string, name string) (*FolderState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var state FolderState
	// Match by ID or case-insensitive Name
	err := s.db.QueryRow(`SELECT id, name, uid_validity, next_uid, delta_link FROM folders WHERE id = ? OR LOWER(name) = LOWER(?)`, folderID, name).Scan(
		&state.ID, &state.Name, &state.UIDValidity, &state.NextUID, &state.DeltaLink,
	)
	if err == nil {
		if state.Name != name || state.ID != folderID {
			_, _ = s.db.Exec(`UPDATE folders SET name = ?, id = ? WHERE id = ?`, name, folderID, state.ID)
			state.Name = name
			state.ID = folderID
		}
		return &state, nil
	}

	if err != sql.ErrNoRows {
		return nil, err
	}

	// Create new folder state with current unix timestamp as UIDVALIDITY
	uidValidity := uint32(time.Now().Unix() & 0x7FFFFFFF)
	if uidValidity == 0 {
		uidValidity = 1
	}

	_, err = s.db.Exec(`INSERT INTO folders (id, name, uid_validity, next_uid, delta_link) VALUES (?, ?, ?, 1, '')`,
		folderID, name, uidValidity,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert folder state: %w", err)
	}

	return &FolderState{
		ID:          folderID,
		Name:        name,
		UIDValidity: uidValidity,
		NextUID:     1,
		DeltaLink:   "",
	}, nil
}

func (s *Store) GetFolderByNameOrID(nameOrID string) (*FolderState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var state FolderState
	err := s.db.QueryRow(`SELECT id, name, uid_validity, next_uid, delta_link FROM folders WHERE id = ?`, nameOrID).Scan(
		&state.ID, &state.Name, &state.UIDValidity, &state.NextUID, &state.DeltaLink,
	)
	if err == nil {
		return &state, nil
	}

	err = s.db.QueryRow(`SELECT id, name, uid_validity, next_uid, delta_link FROM folders WHERE LOWER(name) = LOWER(?)`, nameOrID).Scan(
		&state.ID, &state.Name, &state.UIDValidity, &state.NextUID, &state.DeltaLink,
	)
	if err == nil {
		return &state, nil
	}

	wellKnownID := canonicalWellKnownID(nameOrID)
	if wellKnownID != "" {
		err = s.db.QueryRow(`SELECT id, name, uid_validity, next_uid, delta_link FROM folders WHERE id = ? OR LOWER(name) = LOWER(?)`, wellKnownID, wellKnownID).Scan(
			&state.ID, &state.Name, &state.UIDValidity, &state.NextUID, &state.DeltaLink,
		)
		if err == nil {
			return &state, nil
		}
	}

	return nil, sql.ErrNoRows
}

func canonicalWellKnownID(name string) string {
	lower := strings.ToLower(name)
	switch lower {
	case "inbox":
		return "inbox"
	case "sent items", "sent", "sentitems":
		return "sentitems"
	case "drafts":
		return "drafts"
	case "deleted items", "trash", "deleteditems":
		return "deleteditems"
	default:
		return ""
	}
}

func (s *Store) ResolveFolderID(nameOrID string) string {
	folder, err := s.GetFolderByNameOrID(nameOrID)
	if err == nil && folder != nil {
		return folder.ID
	}
	wellKnown := canonicalWellKnownID(nameOrID)
	if wellKnown != "" {
		return wellKnown
	}
	return nameOrID
}

func (s *Store) SaveDeltaLink(folderID string, deltaLink string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE folders SET delta_link = ? WHERE id = ?`, deltaLink, folderID)
	return err
}

func (s *Store) MapMessage(folderID string, graphID string, subject string, isRead bool, isDraft bool, receivedAt time.Time, size int64) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingUID uint32
	err := s.db.QueryRow(`SELECT uid FROM messages WHERE folder_id = ? AND graph_id = ?`, folderID, graphID).Scan(&existingUID)
	if err == nil {
		// Update existing message flags/metadata
		isReadInt := 0
		if isRead {
			isReadInt = 1
		}
		isDraftInt := 0
		if isDraft {
			isDraftInt = 1
		}
		_, _ = s.db.Exec(`UPDATE messages SET subject = ?, is_read = ?, is_draft = ?, received_at = ?, size = ? WHERE folder_id = ? AND graph_id = ?`,
			subject, isReadInt, isDraftInt, receivedAt.Unix(), size, folderID, graphID,
		)
		return existingUID, nil
	}

	// Get and increment next_uid for folder
	var nextUID uint32
	err = s.db.QueryRow(`SELECT next_uid FROM folders WHERE id = ?`, folderID).Scan(&nextUID)
	if err != nil {
		return 0, fmt.Errorf("folder %s not found: %w", folderID, err)
	}

	uid := nextUID
	nextUID++

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	isReadInt := 0
	if isRead {
		isReadInt = 1
	}
	isDraftInt := 0
	if isDraft {
		isDraftInt = 1
	}

	_, err = tx.Exec(`INSERT INTO messages (folder_id, graph_id, uid, subject, is_read, is_draft, is_deleted, received_at, size) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		folderID, graphID, uid, subject, isReadInt, isDraftInt, receivedAt.Unix(), size,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert message mapping: %w", err)
	}

	_, err = tx.Exec(`UPDATE folders SET next_uid = ? WHERE id = ?`, nextUID, folderID)
	if err != nil {
		return 0, fmt.Errorf("failed to update next_uid: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return uid, nil
}

func (s *Store) UpdateMessageFlags(folderID string, uid uint32, isRead bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	isReadInt := 0
	if isRead {
		isReadInt = 1
	}

	_, err := s.db.Exec(`UPDATE messages SET is_read = ? WHERE folder_id = ? AND uid = ?`, isReadInt, folderID, uid)
	return err
}

func (s *Store) MarkDeleted(folderID string, uid uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE messages SET is_deleted = 1 WHERE folder_id = ? AND uid = ?`, folderID, uid)
	return err
}

func (s *Store) ListMessages(folderID string) ([]MessageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT folder_id, graph_id, uid, subject, is_read, is_draft, is_deleted, received_at, size FROM messages WHERE folder_id = ? ORDER BY uid ASC`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MessageRecord
	for rows.Next() {
		var rec MessageRecord
		var isReadInt, isDraftInt, isDeletedInt int
		var receivedUnix int64

		if err := rows.Scan(&rec.FolderID, &rec.GraphID, &rec.UID, &rec.Subject, &isReadInt, &isDraftInt, &isDeletedInt, &receivedUnix, &rec.Size); err != nil {
			return nil, err
		}
		rec.IsRead = isReadInt == 1
		rec.IsDraft = isDraftInt == 1
		rec.IsDeleted = isDeletedInt == 1
		rec.ReceivedAt = time.Unix(receivedUnix, 0)
		results = append(results, rec)
	}

	return results, nil
}

func (s *Store) GetMessageByUID(folderID string, uid uint32) (*MessageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rec MessageRecord
	var isReadInt, isDraftInt, isDeletedInt int
	var receivedUnix int64

	err := s.db.QueryRow(`SELECT folder_id, graph_id, uid, subject, is_read, is_draft, is_deleted, received_at, size FROM messages WHERE folder_id = ? AND uid = ?`, folderID, uid).Scan(
		&rec.FolderID, &rec.GraphID, &rec.UID, &rec.Subject, &isReadInt, &isDraftInt, &isDeletedInt, &receivedUnix, &rec.Size,
	)
	if err != nil {
		return nil, err
	}

	rec.IsRead = isReadInt == 1
	rec.IsDraft = isDraftInt == 1
	rec.IsDeleted = isDeletedInt == 1
	rec.ReceivedAt = time.Unix(receivedUnix, 0)
	return &rec, nil
}

func (s *Store) ExpungeDeleted(folderID string) ([]uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT uid FROM messages WHERE folder_id = ? AND is_deleted = 1`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var expungedUIDs []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err == nil {
			expungedUIDs = append(expungedUIDs, uid)
		}
	}

	if len(expungedUIDs) > 0 {
		_, _ = s.db.Exec(`DELETE FROM messages WHERE folder_id = ? AND is_deleted = 1`, folderID)
	}

	return expungedUIDs, nil
}
