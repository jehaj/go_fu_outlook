package test

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	netSMTP "net/smtp"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"graph-mail-proxy/internal/config"
	"graph-mail-proxy/internal/graph"
	"graph-mail-proxy/internal/imap"
	"graph-mail-proxy/internal/smtp"
	"graph-mail-proxy/internal/store"

	imapV2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type staticTokenProvider struct{}

func (s *staticTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	return "mock-access-token", nil
}

func TestEndToEndProxySmoke(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e.db")

	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	// Track Graph calls
	var draftCreated bool
	var mailSent bool
	var uploadedBase64 string

	mockGraph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/me/mailFolders" {
			_, _ = w.Write([]byte(`{"value": [{"id": "inbox", "displayName": "Inbox", "unreadItemCount": 1, "totalItemCount": 1}]}`))
			return
		}
		if r.URL.Path == "/me/mailFolders/inbox/messages" {
			_, _ = w.Write([]byte(`{"value": [{"id": "msg100", "subject": "E2E Test Email", "isRead": false, "createdDateTime": "2026-07-29T12:00:00Z"}]}`))
			return
		}
		if r.URL.Path == "/me/messages/msg100/$value" {
			w.Header().Set("Content-Type", "message/rfc822")
			_, _ = w.Write([]byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: E2E Test Email\r\n\r\nE2E Body Content"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/me/messages" {
			draftCreated = true
			bodyBytes, _ := io.ReadAll(r.Body)
			uploadedBase64 = string(bodyBytes)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": "draft_msg_e2e"}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/me/messages/draft_msg_e2e/send" {
			mailSent = true
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer mockGraph.Close()

	graphClient := graph.NewClient(&staticTokenProvider{}, mockGraph.URL)

	cfg := config.DefaultConfig()
	cfg.IMAP.BindAddr = "127.0.0.1:0"
	cfg.SMTP.BindAddr = "127.0.0.1:0"
	cfg.Storage.DataDir = tmpDir

	imapSrv, err := imap.NewServer(cfg, graphClient, st)
	if err != nil {
		t.Fatalf("failed to create IMAP server: %v", err)
	}

	smtpSrv, err := smtp.NewServer(cfg, graphClient)
	if err != nil {
		t.Fatalf("failed to create SMTP server: %v", err)
	}

	go func() { _ = imapSrv.Start() }()
	go func() { _ = smtpSrv.Start() }()

	t.Cleanup(func() {
		_ = imapSrv.Close()
		_ = smtpSrv.Close()
	})

	for i := 0; i < 50; i++ {
		if imapSrv.Addr() != nil && smtpSrv.Addr() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	imapAddr := imapSrv.Addr().String()
	smtpAddr := smtpSrv.Addr().String()

	// Step 1: IMAP Read Flow
	imapCli, err := imapclient.DialInsecure(imapAddr, nil)
	if err != nil {
		t.Fatalf("IMAP dial failed: %v", err)
	}
	defer imapCli.Close()

	if err := imapCli.Login("thunderbird", "localpassword").Wait(); err != nil {
		t.Fatalf("IMAP login failed: %v", err)
	}

	mboxes, err := imapCli.List("", "*", nil).Collect()
	if err != nil || len(mboxes) == 0 {
		t.Fatalf("IMAP List failed (len %d): %v", len(mboxes), err)
	}

	selectData, err := imapCli.Select("INBOX", nil).Wait()
	if err != nil || selectData.NumMessages != 1 {
		t.Fatalf("IMAP Select failed (numMsgs %d): %v", selectData.NumMessages, err)
	}

	fetchCmd := imapCli.Fetch(imapV2.SeqSetNum(1), &imapV2.FetchOptions{Flags: true, UID: true})
	fetched, err := fetchCmd.Collect()
	if err != nil || len(fetched) != 1 {
		t.Fatalf("IMAP Fetch failed: %v", err)
	}
	if fetched[0].UID != 1 {
		t.Errorf("expected UID 1, got %d", fetched[0].UID)
	}

	// Step 2: SMTP Send Flow
	host, _, _ := net.SplitHostPort(smtpAddr)
	smtpAuth := netSMTP.PlainAuth("", "thunderbird", "localpassword", host)

	from := "alice@example.com"
	to := []string{"bob@example.com"}
	rawMIME := []byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: E2E Send Test\r\n\r\nHello from E2E!")

	if err := netSMTP.SendMail(smtpAddr, smtpAuth, from, to, rawMIME); err != nil {
		t.Fatalf("SMTP SendMail failed: %v", err)
	}

	if !draftCreated || !mailSent {
		t.Errorf("E2E mail send failure: draftCreated=%v, mailSent=%v", draftCreated, mailSent)
	}

	decodedMIME, err := base64.StdEncoding.DecodeString(uploadedBase64)
	if err != nil {
		t.Fatalf("failed to decode uploaded base64 MIME: %v", err)
	}
	if !strings.Contains(string(decodedMIME), "E2E Send Test") {
		t.Errorf("uploaded MIME does not contain expected subject: %s", string(decodedMIME))
	}
}
