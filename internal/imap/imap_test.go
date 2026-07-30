package imap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"graph-mail-proxy/internal/config"
	"graph-mail-proxy/internal/graph"
	"graph-mail-proxy/internal/store"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type dummyTokenProvider struct{}

func (d *dummyTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	return "mock-token", nil
}

func setupTestIMAPServer(t *testing.T) (*Server, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	msgs := map[string]bool{"msg1": true}

	// Mock Graph server
	graphServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete && r.URL.Path == "/me/messages/msg1" {
			delete(msgs, "msg1")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/me/mailFolders" {
			_, _ = w.Write([]byte(`{"value": [{"id": "inbox", "displayName": "Inbox", "unreadItemCount": 1, "totalItemCount": 1}]}`))
			return
		}
		if r.URL.Path == "/me/mailFolders/inbox/messages" {
			if msgs["msg1"] {
				_, _ = w.Write([]byte(`{"value": [{"id": "msg1", "subject": "Hello IMAP", "isRead": false, "createdDateTime": "2026-07-29T12:00:00Z"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"value": []}`))
			}
			return
		}
		if r.URL.Path == "/me/messages/msg1/$value" {
			w.Header().Set("Content-Type", "message/rfc822")
			_, _ = w.Write([]byte("From: sender@example.com\r\nTo: user@example.com\r\nSubject: Hello IMAP\r\n\r\nHello IMAP body!"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(func() { graphServer.Close() })

	graphClient := graph.NewClient(&dummyTokenProvider{}, graphServer.URL)

	cfg := config.DefaultConfig()
	cfg.IMAP.BindAddr = "127.0.0.1:0" // Ephemeral port
	cfg.Storage.DataDir = tmpDir

	server, err := NewServer(cfg, graphClient, st)
	if err != nil {
		t.Fatalf("failed to create IMAP server: %v", err)
	}

	go func() {
		_ = server.Start()
	}()

	// Wait for server listener to bind
	for i := 0; i < 50; i++ {
		if server.Addr() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if server.Addr() == nil {
		t.Fatalf("IMAP server failed to bind listener")
	}

	t.Cleanup(func() {
		_ = server.Close()
		_ = st.Close()
	})

	return server, server.Addr().String()
}

func TestIMAPLogin(t *testing.T) {
	_, addr := setupTestIMAPServer(t)

	// Test invalid credentials
	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("failed to dial IMAP server: %v", err)
	}
	defer client.Close()

	if err := client.Login("baduser", "badpass").Wait(); err == nil {
		t.Errorf("expected Login failure with invalid credentials, got nil")
	}

	// Test valid credentials
	client2, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("failed to dial IMAP server: %v", err)
	}
	defer client2.Close()

	if err := client2.Login("thunderbird", "localpassword").Wait(); err != nil {
		t.Fatalf("expected valid login to succeed: %v", err)
	}
}

func TestIMAPListAndSelect(t *testing.T) {
	_, addr := setupTestIMAPServer(t)

	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("failed to dial IMAP server: %v", err)
	}
	defer client.Close()

	if err := client.Login("thunderbird", "localpassword").Wait(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// List mailboxes
	mailboxes, err := client.List("", "*", nil).Collect()
	if err != nil {
		t.Fatalf("failed to list mailboxes: %v", err)
	}

	if len(mailboxes) == 0 {
		t.Fatalf("expected at least 1 mailbox, got 0")
	}

	// Select INBOX
	selectData, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("failed to select INBOX: %v", err)
	}

	if selectData.NumMessages != 1 {
		t.Errorf("expected 1 message in INBOX selectData, got %d", selectData.NumMessages)
	}

	// Fetch message
	fetchOptions := &imap.FetchOptions{
		Flags: true,
		UID:   true,
	}
	seqSet := imap.SeqSetNum(1)
	fetchCmd := client.Fetch(seqSet, fetchOptions)

	fetchedMsgs, err := fetchCmd.Collect()
	if err != nil {
		t.Fatalf("failed to fetch message: %v", err)
	}

	if len(fetchedMsgs) != 1 {
		t.Fatalf("expected 1 fetched message, got %d", len(fetchedMsgs))
	}

	if fetchedMsgs[0].UID != 1 {
		t.Errorf("expected fetched UID = 1, got %d", fetchedMsgs[0].UID)
	}
}

func TestIMAPStoreSearchAndExpunge(t *testing.T) {
	_, addr := setupTestIMAPServer(t)

	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("failed to dial IMAP server: %v", err)
	}
	defer client.Close()

	if err := client.Login("thunderbird", "localpassword").Wait(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	_, err = client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select INBOX failed: %v", err)
	}

	// Test Search
	searchData, err := client.Search(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(searchData.AllSeqNums()) != 1 {
		t.Errorf("expected 1 search result, got %d", len(searchData.AllSeqNums()))
	}

	// Test Store \Seen and \Deleted
	seqSet := imap.SeqSetNum(1)
	storeFlags := &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagDeleted},
	}

	if err := client.Store(seqSet, storeFlags, nil).Close(); err != nil {
		t.Fatalf("store flags failed: %v", err)
	}

	// Test Expunge
	if err := client.Expunge().Close(); err != nil {
		t.Fatalf("expunge failed: %v", err)
	}

	// Verify mailbox is empty after expunge
	selectAfter, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("re-select INBOX failed: %v", err)
	}
	if selectAfter.NumMessages != 0 {
		t.Errorf("expected 0 messages after expunge, got %d", selectAfter.NumMessages)
	}
}

func TestIMAPCustomFolderAndPagination(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	var graphServer *httptest.Server
	graphServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/me/mailFolders" {
			resp := `{
				"value": [
					{"id": "inbox", "displayName": "Inbox", "childFolderCount": 0},
					{"id": "graph_id_brightspace", "displayName": "BrightSpace", "childFolderCount": 0}
				]
			}`
			_, _ = w.Write([]byte(resp))
			return
		}

		if r.URL.Path == "/me/mailFolders/graph_id_brightspace/messages" {
			page2URL := graphServer.URL + "/me/mailFolders/graph_id_brightspace/messages/page2"
			// Page 1: generate 100 messages with nextLink
			var msgs []string
			for i := 1; i <= 100; i++ {
				msgs = append(msgs, `{"id": "bs_msg_`+string(rune('0'+i%10))+`_`+string(rune('a'+i%26))+`_`+string(rune('A'+i%26))+`", "subject": "BrightSpace Email", "createdDateTime": "2026-07-29T12:00:00Z"}`)
			}
			jsonMsgs := `[` + strings.Join(msgs, ",") + `]`
			resp := `{
				"value": ` + jsonMsgs + `,
				"@odata.nextLink": "` + page2URL + `"
			}`
			_, _ = w.Write([]byte(resp))
			return
		}

		if r.URL.Path == "/me/mailFolders/graph_id_brightspace/messages/page2" {
			// Page 2: generate 5 messages without nextLink
			var msgs []string
			for i := 101; i <= 105; i++ {
				msgs = append(msgs, `{"id": "bs_msg_p2_`+string(rune('0'+i%10))+`_`+string(rune('a'+i%26))+`", "subject": "BrightSpace Email Page 2", "createdDateTime": "2026-07-29T12:00:00Z"}`)
			}
			jsonMsgs := `[` + strings.Join(msgs, ",") + `]`
			resp := `{
				"value": ` + jsonMsgs + `
			}`
			_, _ = w.Write([]byte(resp))
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(func() { graphServer.Close() })

	graphClient := graph.NewClient(&dummyTokenProvider{}, graphServer.URL)
	cfg := config.DefaultConfig()
	cfg.IMAP.BindAddr = "127.0.0.1:0"
	cfg.Storage.DataDir = tmpDir

	server, err := NewServer(cfg, graphClient, st)
	if err != nil {
		t.Fatalf("failed to create IMAP server: %v", err)
	}

	go func() { _ = server.Start() }()
	for i := 0; i < 50; i++ {
		if server.Addr() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		_ = server.Close()
		_ = st.Close()
	})

	client, err := imapclient.DialInsecure(server.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer client.Close()

	if err := client.Login("thunderbird", "localpassword").Wait(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// Verify List returns BrightSpace
	mboxes, err := client.List("", "*", nil).Collect()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	foundBS := false
	for _, mb := range mboxes {
		if mb.Mailbox == "BrightSpace" {
			foundBS = true
		}
	}
	if !foundBS {
		t.Errorf("expected BrightSpace in mailbox list, got %v", mboxes)
	}

	// Select BrightSpace and verify all 105 messages are synced!
	selectData, err := client.Select("BrightSpace", nil).Wait()
	if err != nil {
		t.Fatalf("Select BrightSpace failed: %v", err)
	}

	if selectData.NumMessages != 105 {
		t.Errorf("expected 105 messages in BrightSpace, got %d", selectData.NumMessages)
	}
}
