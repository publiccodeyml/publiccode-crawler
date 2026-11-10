package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadPublishersKeepsDestinationOnUnsuccessfulResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "publishers.yml")
	original := []byte("- id: existing\n  name: Existing publisher\n")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}

	err := downloadPublishers(server.URL, destination)
	if err == nil {
		t.Fatal("downloadPublishers() returned nil error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("downloadPublishers() error = %v, want HTTP status", err)
	}

	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("destination = %q, want %q", data, original)
	}
}
