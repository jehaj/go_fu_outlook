package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig validation failed: %v", err)
	}

	if cfg.Azure.ClientID != DefaultClientID {
		t.Errorf("expected default client ID %s, got %s", DefaultClientID, cfg.Azure.ClientID)
	}
	if cfg.IMAP.BindAddr != DefaultIMAPBindAddr {
		t.Errorf("expected default IMAP bind %s, got %s", DefaultIMAPBindAddr, cfg.IMAP.BindAddr)
	}
	if cfg.SMTP.BindAddr != DefaultSMTPBindAddr {
		t.Errorf("expected default SMTP bind %s, got %s", DefaultSMTPBindAddr, cfg.SMTP.BindAddr)
	}
}

func TestLoopbackValidation(t *testing.T) {
	validAddrs := []string{
		"127.0.0.1:1143",
		"127.0.0.1:1025",
		"localhost:1143",
		"[::1]:1143",
	}

	for _, addr := range validAddrs {
		cfg := DefaultConfig()
		cfg.IMAP.BindAddr = addr
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected address %q to be valid loopback, got err: %v", addr, err)
		}
	}

	invalidAddrs := []string{
		"0.0.0.0:1143",
		"192.168.1.50:1143",
		"10.0.0.1:1025",
		"example.com:1143",
		"8.8.8.8:1025",
	}

	for _, addr := range invalidAddrs {
		cfg := DefaultConfig()
		cfg.IMAP.BindAddr = addr
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected address %q to be rejected as non-loopback, but passed validation", addr)
		}
	}
}

func TestLoadConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
azure:
  tenant_id: "test-tenant"
  client_id: "test-client-id"
imap:
  bind_addr: "127.0.0.1:2143"
smtp:
  bind_addr: "127.0.0.1:2025"
local_auth:
  username: "testuser"
  password: "secretpassword"
sync:
  poll_interval: "30s"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config file: %v", err)
	}

	if cfg.Azure.TenantID != "test-tenant" {
		t.Errorf("expected tenant_id 'test-tenant', got %q", cfg.Azure.TenantID)
	}
	if cfg.Azure.ClientID != "test-client-id" {
		t.Errorf("expected client_id 'test-client-id', got %q", cfg.Azure.ClientID)
	}
	if cfg.IMAP.BindAddr != "127.0.0.1:2143" {
		t.Errorf("expected IMAP bind_addr '127.0.0.1:2143', got %q", cfg.IMAP.BindAddr)
	}
	if cfg.Sync.PollInterval != "30s" {
		t.Errorf("expected poll_interval '30s', got %q", cfg.Sync.PollInterval)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot get user home dir: %v", err)
	}

	expanded := ExpandPath("~/.config/graph-mail-proxy")
	expected := filepath.Join(home, ".config/graph-mail-proxy")
	if expanded != expected {
		t.Errorf("expected expanded path %q, got %q", expected, expanded)
	}

	nonTilde := "/var/log/proxy.log"
	if ExpandPath(nonTilde) != nonTilde {
		t.Errorf("expected path %q to remain unchanged", nonTilde)
	}
}
