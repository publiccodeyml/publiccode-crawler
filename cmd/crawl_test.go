package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func TestCrawlForgejoSourceWithoutGitHubToken(t *testing.T) {
	var reposListed atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/swagger.v1.json":
			_, _ = w.Write([]byte(`{"info": {"title": "Forgejo API"}}`))
		case "/api/v1/orgs/org/repos":
			reposListed.Store(true)

			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	publishers := filepath.Join(t.TempDir(), "publishers.yml")
	content := "- id: forgejo\n  name: Forgejo\n  orgs:\n    - " + server.URL + "/org\n"

	if err := os.WriteFile(publishers, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	viper.Set("GITHUB_TOKEN", "")
	viper.Set("DATADIR", t.TempDir())
	viper.Set("API_BASEURL", "")
	t.Cleanup(viper.Reset)

	// A fatal log would otherwise exit the test binary.
	logger := log.StandardLogger()
	exitFunc := logger.ExitFunc
	t.Cleanup(func() { logger.ExitFunc = exitFunc })

	logger.ExitFunc = func(code int) {
		t.Fatalf("crawl exited with code %d", code)
	}

	rootCmd.SetArgs([]string{"crawl", "--dry-run", publishers})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !reposListed.Load() {
		t.Fatal("crawl did not list the Forgejo organization repositories")
	}
}
