package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"graph-mail-proxy/internal/config"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		absent   string
	}{
		{
			name:     "Bearer Token Redaction",
			input:    "HTTP Request Header: Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.secretpayload",
			contains: "Authorization: [REDACTED]",
			absent:   "secretpayload",
		},
		{
			name:     "Authorization Header Redaction",
			input:    "Sending request with Authorization: Basic dXNlcm5hbWU6cGFzc3dvcmQ=",
			contains: "Authorization: [REDACTED]",
			absent:   "dXNlcm5hbWU6cGFzc3dvcmQ=",
		},
		{
			name:     "JSON Token Redaction",
			input:    `{"access_token": "secret_access_token_value", "token_type": "Bearer"}`,
			contains: `"access_token": "[REDACTED]"`,
			absent:   "secret_access_token_value",
		},
		{
			name:     "KV Password Redaction",
			input:    "connect failed with password=supersecretpassword123 for user",
			contains: "password=[REDACTED]",
			absent:   "supersecretpassword123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("expected redacted output to contain %q, got %q", tt.contains, got)
			}
			if strings.Contains(got, tt.absent) {
				t.Errorf("expected redacted output to NOT contain %q, got %q", tt.absent, got)
			}
		})
	}
}

func TestFileTokenCachePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "subfolder", "tokens.json")

	fileCache := NewFileTokenCache(tokenFile)

	// Test Exporting dummy cache data
	dummyMarshaler := &mockMarshaler{data: []byte("test_token_cache_data")}
	if err := fileCache.Export(context.Background(), dummyMarshaler, cache.ExportHints{}); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify directory permissions (0700)
	dirInfo, err := os.Stat(filepath.Dir(tokenFile))
	if err != nil {
		t.Fatalf("failed to stat token directory: %v", err)
	}
	if dirInfo.Mode().Perm()&0077 != 0 {
		t.Errorf("expected token dir permissions 0700, got %o", dirInfo.Mode().Perm())
	}

	// Verify file permissions (0600)
	fileInfo, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("failed to stat token file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("expected token file permissions 0600, got %o", fileInfo.Mode().Perm())
	}

	// Test Replace unmarshals file
	dummyUnmarshaler := &mockUnmarshaler{}
	if err := fileCache.Replace(context.Background(), dummyUnmarshaler, cache.ReplaceHints{}); err != nil {
		t.Fatalf("Replace failed: %v", err)
	}
	if string(dummyUnmarshaler.data) != "test_token_cache_data" {
		t.Errorf("expected unmarshaled data 'test_token_cache_data', got %q", string(dummyUnmarshaler.data))
	}
}

func TestFileTokenCacheMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "nonexistent_tokens.json")

	fileCache := NewFileTokenCache(tokenFile)
	dummyUnmarshaler := &mockUnmarshaler{}
	if err := fileCache.Replace(context.Background(), dummyUnmarshaler, cache.ReplaceHints{}); err != nil {
		t.Fatalf("expected Replace on missing file to return nil, got err: %v", err)
	}
}

func TestAuthManagerInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = tmpDir

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("failed to create AuthManager: %v", err)
	}

	if am.TokenFilePath() != filepath.Join(tmpDir, "tokens.json") {
		t.Errorf("expected token file path %s, got %s", filepath.Join(tmpDir, "tokens.json"), am.TokenFilePath())
	}

	_, err = am.GetAccessToken(context.Background())
	if err == nil {
		t.Errorf("expected GetAccessToken to return error when cache is empty, got nil")
	}
}

type mockMarshaler struct {
	data []byte
}

func (m *mockMarshaler) Marshal() ([]byte, error) {
	return m.data, nil
}

type mockUnmarshaler struct {
	data []byte
}

func (u *mockUnmarshaler) Unmarshal(data []byte) error {
	u.data = data
	return nil
}
