package main

import (
	"testing"
)

func TestDefaultClientIDs(t *testing.T) {
	expectedKeys := []string{"office", "teams", "azure-cli", "azure-ps"}
	for _, key := range expectedKeys {
		id, ok := defaultClientIDs[key]
		if !ok || id == "" {
			t.Errorf("expected client ID alias %q to exist and be non-empty", key)
		}
	}
}
