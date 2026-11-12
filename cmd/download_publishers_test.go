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

	"github.com/italia/publiccode-crawler/v4/common"
	"go.yaml.in/yaml/v3"
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

func newRepolistServer(t *testing.T, requests *int) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*requests++

		_, _ = w.Write([]byte("registrati:\n  - ipa: newcomer\n    url: https://github.com/newcomer\n"))
	}))
	t.Cleanup(server.Close)

	return server
}

func readPublishers(t *testing.T, path string) []common.Publisher {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var publishers []common.Publisher
	//nolint:musttag // false positive
	if err := yaml.Unmarshal(data, &publishers); err != nil {
		t.Fatal(err)
	}

	return publishers
}

func TestDownloadPublishersKeepsMalformedDestination(t *testing.T) {
	t.Parallel()

	requests := 0
	server := newRepolistServer(t, &requests)

	destination := filepath.Join(t.TempDir(), "publishers.yml")
	original := []byte("- id: existing\n  name: [unterminated\n")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}

	err := downloadPublishers(context.Background(), server.URL, destination)
	if err == nil {
		t.Fatal("downloadPublishers() returned nil error")
	}
	if !strings.Contains(err.Error(), "parse existing publishers") {
		t.Fatalf("downloadPublishers() error = %v, want parse error", err)
	}
	if requests != 0 {
		t.Fatalf("portal requests = %d, want 0", requests)
	}

	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("destination = %q, want %q", data, original)
	}
}

func TestDownloadPublishersMergesValidDestination(t *testing.T) {
	t.Parallel()

	requests := 0
	server := newRepolistServer(t, &requests)

	destination := filepath.Join(t.TempDir(), "publishers.yml")
	original := []byte("- id: existing\n  name: Existing publisher\n")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := downloadPublishers(context.Background(), server.URL, destination); err != nil {
		t.Fatalf("downloadPublishers() error = %v", err)
	}

	publishers := readPublishers(t, destination)
	if len(publishers) != 2 || publishers[0].ID != "existing" || publishers[1].ID != "newcomer" {
		t.Fatalf("publishers = %+v, want existing and newcomer", publishers)
	}
}

func TestDownloadPublishersCreatesMissingDestination(t *testing.T) {
	t.Parallel()

	requests := 0
	server := newRepolistServer(t, &requests)

	destination := filepath.Join(t.TempDir(), "publishers.yml")

	if err := downloadPublishers(context.Background(), server.URL, destination); err != nil {
		t.Fatalf("downloadPublishers() error = %v", err)
	}

	publishers := readPublishers(t, destination)
	if len(publishers) != 1 || publishers[0].ID != "newcomer" {
		t.Fatalf("publishers = %+v, want newcomer", publishers)
	}
}
