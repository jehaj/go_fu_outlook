package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// FileTokenCache implements MSAL cache.Exportable interface to persist token cache to disk securely.
type FileTokenCache struct {
	filePath string
	mu       sync.Mutex
}

func NewFileTokenCache(filePath string) *FileTokenCache {
	return &FileTokenCache{
		filePath: filePath,
	}
}

func (c *FileTokenCache) Replace(ctx context.Context, unmarshaler cache.Unmarshaler, hints cache.ReplaceHints) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read token cache file: %w", err)
	}

	// Verify permissions are not world-readable if file exists
	info, err := os.Stat(c.filePath)
	if err == nil {
		perm := info.Mode().Perm()
		if perm&0077 != 0 {
			// Enforce strict 0600 permission
			_ = os.Chmod(c.filePath, 0600)
		}
	}

	if err := unmarshaler.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal token cache: %w", err)
	}

	return nil
}

func (c *FileTokenCache) Export(ctx context.Context, marshaler cache.Marshaler, hints cache.ExportHints) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := marshaler.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal token cache: %w", err)
	}

	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create token directory: %w", err)
	}

	// Write atomically with 0600 permissions
	tmpFile := c.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp token file: %w", err)
	}

	if err := os.Rename(tmpFile, c.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to commit token file: %w", err)
	}

	return os.Chmod(c.filePath, 0600)
}

func (c *FileTokenCache) FilePath() string {
	return c.filePath
}
