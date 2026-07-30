package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFolderUIDValidityPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store1, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store1: %v", err)
	}

	state1, err := store1.EnsureFolder("inbox", "Inbox")
	if err != nil {
		t.Fatalf("failed to ensure folder: %v", err)
	}

	if state1.UIDValidity == 0 {
		t.Errorf("expected non-zero UIDValidity")
	}
	if state1.NextUID != 1 {
		t.Errorf("expected initial NextUID to be 1, got %d", state1.NextUID)
	}
	_ = store1.Close()

	// Reopen store from same DB file (simulating app restart)
	store2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store2: %v", err)
	}
	defer store2.Close()

	state2, err := store2.EnsureFolder("inbox", "Inbox")
	if err != nil {
		t.Fatalf("failed to ensure folder on restart: %v", err)
	}

	if state2.UIDValidity != state1.UIDValidity {
		t.Errorf("UIDValidity changed across restart: original %d, new %d", state1.UIDValidity, state2.UIDValidity)
	}
}

func TestUIDSequenceMapping(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	_, _ = s.EnsureFolder("inbox", "Inbox")

	now := time.Now()
	uid1, err := s.MapMessage("inbox", "graph_id_101", "Subject 1", false, false, now, 1024)
	if err != nil || uid1 != 1 {
		t.Errorf("expected uid1 = 1, got %d (err: %v)", uid1, err)
	}

	uid2, err := s.MapMessage("inbox", "graph_id_102", "Subject 2", true, false, now, 2048)
	if err != nil || uid2 != 2 {
		t.Errorf("expected uid2 = 2, got %d (err: %v)", uid2, err)
	}

	// Re-map graph_id_101 with updated read flag
	uid1Again, err := s.MapMessage("inbox", "graph_id_101", "Subject 1 Updated", true, false, now, 1024)
	if err != nil || uid1Again != 1 {
		t.Errorf("expected re-mapped graph_id_101 to keep uid = 1, got %d", uid1Again)
	}

	messages, err := s.ListMessages("inbox")
	if err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if !messages[0].IsRead || messages[0].Subject != "Subject 1 Updated" {
		t.Errorf("message 1 flags/subject not updated properly: %+v", messages[0])
	}
}

func TestFlagUpdatesAndDeletion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	_, _ = s.EnsureFolder("inbox", "Inbox")
	now := time.Now()

	uid, err := s.MapMessage("inbox", "graph_msg_1", "Test Subject", false, false, now, 512)
	if err != nil {
		t.Fatalf("failed to map message: %v", err)
	}

	// Update isRead to true
	if err := s.UpdateMessageFlags("inbox", uid, true); err != nil {
		t.Fatalf("failed to update message flags: %v", err)
	}

	msg, err := s.GetMessageByUID("inbox", uid)
	if err != nil {
		t.Fatalf("failed to get message by UID: %v", err)
	}
	if !msg.IsRead {
		t.Errorf("expected IsRead = true")
	}

	// Mark deleted
	if err := s.MarkDeleted("inbox", uid); err != nil {
		t.Fatalf("failed to mark deleted: %v", err)
	}

	msgDeleted, _ := s.GetMessageByUID("inbox", uid)
	if !msgDeleted.IsDeleted {
		t.Errorf("expected IsDeleted = true")
	}

	// Expunge deleted
	expunged, err := s.ExpungeDeleted("inbox")
	if err != nil {
		t.Fatalf("failed to expunge deleted: %v", err)
	}
	if len(expunged) != 1 || expunged[0] != uid {
		t.Errorf("expected expunged UIDs [%d], got %v", uid, expunged)
	}

	messagesAfter, _ := s.ListMessages("inbox")
	if len(messagesAfter) != 0 {
		t.Errorf("expected 0 messages after expunge, got %d", len(messagesAfter))
	}
}

func TestDeltaLinkPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	folder, _ := s.EnsureFolder("inbox", "Inbox")
	if folder.DeltaLink != "" {
		t.Errorf("expected empty initial delta link")
	}

	newDelta := "https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?deltatoken=xyz123"
	if err := s.SaveDeltaLink("inbox", newDelta); err != nil {
		t.Fatalf("failed to save delta link: %v", err)
	}

	folderAfter, _ := s.EnsureFolder("inbox", "Inbox")
	if folderAfter.DeltaLink != newDelta {
		t.Errorf("expected delta link %q, got %q", newDelta, folderAfter.DeltaLink)
	}
}

func TestFolderResolutionAndAliases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	// Ensure custom folder with Graph ID "AAMk_brightspace" and DisplayName "BrightSpace"
	_, err = s.EnsureFolder("AAMk_brightspace", "BrightSpace")
	if err != nil {
		t.Fatalf("EnsureFolder failed: %v", err)
	}

	// 1. Resolve by exact DisplayName
	resID := s.ResolveFolderID("BrightSpace")
	if resID != "AAMk_brightspace" {
		t.Errorf("expected AAMk_brightspace, got %s", resID)
	}

	// 2. Resolve case-insensitively
	resIDLower := s.ResolveFolderID("brightspace")
	if resIDLower != "AAMk_brightspace" {
		t.Errorf("expected AAMk_brightspace, got %s", resIDLower)
	}

	// 3. Resolve well-known aliases
	resSent := s.ResolveFolderID("Sent Items")
	if resSent != "sentitems" {
		t.Errorf("expected sentitems, got %s", resSent)
	}

	resTrash := s.ResolveFolderID("Trash")
	if resTrash != "deleteditems" {
		t.Errorf("expected deleteditems, got %s", resTrash)
	}
}
