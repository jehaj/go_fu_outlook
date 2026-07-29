package smtp

import (
	"context"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"graph-mail-proxy/internal/config"
)

type mockMailSender struct {
	sentMIME []byte
}

func (m *mockMailSender) SendMail(ctx context.Context, rawMIME []byte) error {
	m.sentMIME = rawMIME
	return nil
}

func setupTestSMTPServer(t *testing.T) (*Server, *mockMailSender, string) {
	cfg := config.DefaultConfig()
	cfg.SMTP.BindAddr = "127.0.0.1:0"

	sender := &mockMailSender{}
	server, err := NewServer(cfg, sender)
	if err != nil {
		t.Fatalf("failed to create SMTP server: %v", err)
	}

	go func() {
		_ = server.Start()
	}()

	for i := 0; i < 50; i++ {
		if server.Addr() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if server.Addr() == nil {
		t.Fatalf("SMTP server failed to bind")
	}

	t.Cleanup(func() {
		_ = server.Close()
	})

	return server, sender, server.Addr().String()
}

func TestSMTPSendMail(t *testing.T) {
	_, mockSender, addr := setupTestSMTPServer(t)

	host, _, _ := net.SplitHostPort(addr)
	auth := smtp.PlainAuth("", "thunderbird", "localpassword", host)

	from := "alice@example.com"
	to := []string{"bob@example.com"}
	msg := []byte("From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Test SMTP Send\r\n\r\nHello via local SMTP proxy!")

	err := smtp.SendMail(addr, auth, from, to, msg)
	if err != nil {
		t.Fatalf("smtp.SendMail failed: %v", err)
	}

	if strings.TrimSpace(string(mockSender.sentMIME)) != strings.TrimSpace(string(msg)) {
		t.Errorf("expected sent MIME %q, got %q", string(msg), string(mockSender.sentMIME))
	}
}

func TestSMTPAuthFailure(t *testing.T) {
	_, _, addr := setupTestSMTPServer(t)

	host, _, _ := net.SplitHostPort(addr)
	auth := smtp.PlainAuth("", "thunderbird", "wrongpassword", host)

	from := "alice@example.com"
	to := []string{"bob@example.com"}
	msg := []byte("Subject: Fail Test\r\n\r\nContent")

	err := smtp.SendMail(addr, auth, from, to, msg)
	if err == nil {
		t.Errorf("expected authentication failure error, got nil")
	}
}
