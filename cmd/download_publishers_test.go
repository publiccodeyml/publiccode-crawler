package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublisherDownloadClientSetsTimeout(t *testing.T) {
	t.Parallel()

	if publisherDownloadHTTPClient.Timeout != publisherDownloadTimeout {
		t.Fatalf("publisherDownloadHTTPClient.Timeout = %s, want %s", publisherDownloadHTTPClient.Timeout, publisherDownloadTimeout)
	}
}

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

	err := downloadPublishers(context.Background(), server.URL, destination)
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

func TestDownloadPublishersCancelsStalledResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := downloadPublishers(ctx, server.URL, t.TempDir()+"/publishers.yml")
	if err == nil {
		t.Fatal("downloadPublishers() returned nil error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("downloadPublishers() error = %v, want context deadline exceeded", err)
	}
}
