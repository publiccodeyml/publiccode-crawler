package crawler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/italia/publiccode-crawler/v4/apiclient"
	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/italia/publiccode-crawler/v4/metrics"
	publiccode "github.com/publiccodeyml/libpubliccode/v5"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeNewAliases_addsNew(t *testing.T) {
	existing := []string{"https://github.com/org/repo.git"}
	newAliases := []string{"https://github.com/org/repo"}

	got := mergeNewAliases(existing, newAliases)

	assert.Equal(t, []string{
		"https://github.com/org/repo.git",
		"https://github.com/org/repo",
	}, got)
}

func TestMergeNewAliases_noDuplicates(t *testing.T) {
	existing := []string{"https://github.com/org/repo.git"}
	newAliases := []string{"https://github.com/org/repo.git"}

	got := mergeNewAliases(existing, newAliases)

	assert.Equal(t, []string{"https://github.com/org/repo.git"}, got)
}

func TestMergeNewAliases_emptyExisting(t *testing.T) {
	got := mergeNewAliases(nil, []string{"https://github.com/org/repo"})

	assert.Equal(t, []string{"https://github.com/org/repo"}, got)
}

func TestMergeNewAliases_emptyNew(t *testing.T) {
	existing := []string{"https://github.com/org/repo"}

	got := mergeNewAliases(existing, nil)

	assert.Equal(t, existing, got)
}

func TestMergeNewAliases_bothEmpty(t *testing.T) {
	got := mergeNewAliases(nil, nil)

	assert.Nil(t, got)
}

func TestMergeNewAliases_multipleNew(t *testing.T) {
	existing := []string{"a"}
	newAliases := []string{"b", "a", "c"}

	got := mergeNewAliases(existing, newAliases)

	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func newPublicCode(t *testing.T, repoURL, organisationURI string) publiccode.PublicCodeV0 {
	t.Helper()

	u, err := url.Parse(repoURL)
	assert.NoError(t, err)

	return publiccode.PublicCodeV0{
		URL: (*publiccode.URL)(u),
		Organisation: &publiccode.OrganisationV0{
			URI: organisationURI,
		},
	}
}

func TestValidateFile_NoAlternativeIDSkipsCheck(t *testing.T) {
	pc := newPublicCode(t,
		"https://github.com/mastodon/mastodon",
		"https://joinmastodon.org",
	)
	publisher := common.Publisher{
		ID:   "9b9aa9c5-30b1-4e56-b3c0-5b6e2e6d3b22",
		Name: "Mastodon community",
	}

	err := validateFile("urn:x-italian-pa:", publisher, pc,
		"https://raw.githubusercontent.com/mastodon/mastodon/main/publiccode.yml")

	assert.NoError(t, err)
}

// With no namespace, the expected value is the bare alternativeId.
func TestValidateFile_NoNamespaceMatchesAlternativeID(t *testing.T) {
	pc := newPublicCode(t,
		"https://github.com/foo/bar",
		"pcm",
	)
	publisher := common.Publisher{ID: "pcm", AlternativeID: "pcm", Name: "PCM"}

	err := validateFile("", publisher, pc,
		"https://raw.githubusercontent.com/foo/bar/main/publiccode.yml")

	assert.NoError(t, err)
}

func TestValidateFile_NoNamespaceMismatch(t *testing.T) {
	pc := newPublicCode(t,
		"https://github.com/foo/bar",
		"https://example.com",
	)
	publisher := common.Publisher{ID: "pcm", AlternativeID: "pcm", Name: "PCM"}

	err := validateFile("", publisher, pc,
		"https://raw.githubusercontent.com/foo/bar/main/publiccode.yml")

	assert.Error(t, err)
}

func TestValidateFile_OrganisationMatchesPublisher(t *testing.T) {
	pc := newPublicCode(t,
		"https://github.com/foo/bar",
		"urn:x-italian-pa:pcm",
	)
	publisher := common.Publisher{ID: "pcm", AlternativeID: "pcm", Name: "PCM"}

	err := validateFile("urn:x-italian-pa:", publisher, pc,
		"https://raw.githubusercontent.com/foo/bar/main/publiccode.yml")

	assert.NoError(t, err)
}

func TestValidateFile_OrganisationMismatch(t *testing.T) {
	pc := newPublicCode(t,
		"https://github.com/foo/bar",
		"urn:x-italian-pa:wrong",
	)
	publisher := common.Publisher{ID: "pcm", AlternativeID: "pcm", Name: "PCM"}

	err := validateFile("urn:x-italian-pa:", publisher, pc,
		"https://raw.githubusercontent.com/foo/bar/main/publiccode.yml")

	assert.Error(t, err)
}

func newTestCrawler(t *testing.T, handler http.HandlerFunc) *Crawler {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	viper.Set("API_BASEURL", server.URL)
	viper.Set("API_BEARER_TOKEN", "token")

	return &Crawler{Index: "test", apiClient: apiclient.NewClient()}
}

func TestUpsertSoftware_reactivatesOnValidFile(t *testing.T) {
	var patched map[string]any

	crawler := newTestCrawler(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)

		bytes, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(bytes, &patched))

		_, _ = w.Write([]byte(`{}`))
	})

	software := &apiclient.Software{ID: "id1", Active: false}
	err := crawler.upsertSoftware("", software, "https://example.org/repo.git", nil, []byte("yml"), true)

	assert.NoError(t, err)
	assert.Equal(t, true, patched["active"])
}

func TestUpsertSoftware_skipsInactiveWithInvalidFile(t *testing.T) {
	requests := 0

	crawler := newTestCrawler(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++

		_, _ = w.Write([]byte(`{}`))
	})

	software := &apiclient.Software{ID: "id1", Active: false}
	err := crawler.upsertSoftware("", software, "https://example.org/repo.git", nil, []byte("yml"), false)

	assert.NoError(t, err)
	assert.Equal(t, 0, requests)
}

func TestUpsertSoftware_keepsActiveOnInvalidFile(t *testing.T) {
	var patched map[string]any

	crawler := newTestCrawler(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)

		bytes, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(bytes, &patched))

		_, _ = w.Write([]byte(`{}`))
	})

	software := &apiclient.Software{ID: "id1", Active: true}
	err := crawler.upsertSoftware("", software, "https://example.org/repo.git", nil, []byte("yml"), false)

	assert.NoError(t, err)
	assert.Equal(t, true, patched["active"])
}

func TestProcessRepo_usesCanonicalURLAfterParseFailure(t *testing.T) {
	dataDir := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "repository")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	runGit(t, gitDir, "init", "--initial-branch=main")
	runGit(t, gitDir, "config", "user.email", "crawler@example.test")
	runGit(t, gitDir, "config", "user.name", "Crawler test")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "README.md"), []byte("test\n"), 0o600))
	runGit(t, gitDir, "add", "README.md")
	runGit(t, gitDir, "commit", "-m", "initial commit")

	var savedURL string
	var savedActive bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/publiccode.yml":
			_, _ = w.Write([]byte("not: [valid"))
		case r.URL.Path == "/software" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.URL.Path == "/software" && r.Method == http.MethodPost:
			var software apiclient.Software
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&software))
			savedURL = software.URL
			savedActive = software.Active
			_, _ = w.Write([]byte(`{"id":"software"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)

	viper.Set("API_BASEURL", server.URL)
	viper.Set("API_BEARER_TOKEN", "token")
	viper.Set("DATADIR", dataDir)
	viper.Set("MAIN_PUBLISHER_ID", "")
	viper.Set("SKIP_VITALITY", false)
	t.Cleanup(viper.Reset)

	canonicalURL, err := url.Parse("file://" + gitDir)
	require.NoError(t, err)
	repositoryURL, err := url.Parse("https://example.test/owner/renamed-repository")
	require.NoError(t, err)

	crawler := &Crawler{
		Index:     "canonical-url",
		apiClient: apiclient.NewClient(),
		newParser: func(config publiccode.ParserConfig) (*publiccode.Parser, error) {
			config.AllowNetworkToPrivateHosts = true

			return publiccode.NewParser(config)
		},
	}
	repository := common.Repository{
		Name:         "owner/repository",
		URL:          *repositoryURL,
		CanonicalURL: *canonicalURL,
		FileRawURL:   server.URL + "/publiccode.yml",
	}

	assert.NotPanics(t, func() {
		crawler.ProcessRepo(repository)
	})
	assert.Equal(t, canonicalURL.String(), savedURL)
	assert.False(t, savedActive)
	assert.FileExists(t, filepath.Join(dataDir, "repos", repositoryURL.Host, "owner", "repository", "vitality.json"))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	command := exec.Command("git", args...)
	command.Dir = dir
	require.NoError(t, command.Run())
}

func TestProcessRepo_validatesFetchedFile(t *testing.T) {
	const template = `publiccodeYmlVersion: "0"
name: Crawler test
url: "REPOSITORY_URL"
releaseDate: "2026-01-01"
platforms: [web]
developmentStatus: stable
softwareType: standalone/other
description:
  en:
    shortDescription: A test application
    longDescription: An application used to check that the crawler validates and stores the file it has already downloaded. Its description and relative screenshot must survive validation without fetching a second copy of the manifest.
    features: [Crawling repositories]
    screenshots: [assets/screenshot.png]
legal:
  license: AGPL-3.0-or-later
maintenance:
  type: none
localisation:
  localisationReady: false
  availableLanguages: [en]
`
	for _, publisherID := range []string{"main", "other"} {
		t.Run(publisherID, func(t *testing.T) {
			viper.Set("MAIN_PUBLISHER_ID", "main")
			viper.Set("SKIP_VITALITY", true)
			t.Cleanup(viper.Reset)

			var manifest string
			var downloads, screenshots atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api":
					http.SetCookie(w, &http.Cookie{Name: "_gitlab_session", Value: "test"})
				case "/org/repo":
					w.WriteHeader(http.StatusOK)
				case "/raw/main/publiccode.yml":
					if downloads.Add(1) > 1 {
						http.Error(w, "rate limited", http.StatusTooManyRequests)

						return
					}
					assert.Equal(t, "file-token", r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(manifest))
				case "/raw/main/assets/screenshot.png":
					screenshots.Add(1)
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected asset request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			manifest = strings.ReplaceAll(template, "REPOSITORY_URL", server.URL+"/org/repo")

			var saved apiclient.Software
			var patches int
			crawler := newTestCrawler(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/software":
					_, _ = w.Write([]byte(`{"data":[{"id":"id1","active":true}]}`))
				case r.Method == http.MethodPatch && r.URL.Path == "/software/id1":
					patches++
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&saved))
					_, _ = w.Write([]byte(`{}`))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/logs"):
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
				}
			})
			crawler.newParser = func(config publiccode.ParserConfig) (*publiccode.Parser, error) {
				assert.Equal(t, server.URL+"/raw/main", config.BaseURL)
				config.AllowNetworkToPrivateHosts = true

				return publiccode.NewParser(config)
			}
			repoURL, err := url.Parse(server.URL + "/org/repo")
			require.NoError(t, err)
			before := metrics.GetCounterValue("repository_good_publiccodeyml", crawler.Index)
			crawler.ProcessRepo(common.Repository{
				Name: "org/repo", URL: *repoURL, CanonicalURL: *repoURL,
				FileRawURL: server.URL + "/raw/main/publiccode.yml",
				Headers:    map[string]string{"Authorization": "file-token"},
				Publisher:  common.Publisher{ID: publisherID},
			})

			assert.EqualValues(t, 1, downloads.Load())
			assert.Positive(t, screenshots.Load(), "relative screenshots must be checked beside the manifest")
			assert.Equal(t, before+1, metrics.GetCounterValue("repository_good_publiccodeyml", crawler.Index))
			require.Equal(t, 1, patches)
			assert.True(t, saved.Active)
			assert.Equal(t, repoURL.String(), saved.URL)

			parser, err := publiccode.NewParser(publiccode.ParserConfig{DisableNetwork: true, DisableExternalChecks: true})
			require.NoError(t, err)
			original, err := parser.ParseStream(strings.NewReader(manifest))
			require.NoError(t, err)
			stored, err := parser.ParseStream(strings.NewReader(saved.PubliccodeYml))
			require.NoError(t, err)
			assert.Equal(t, original, stored)
		})
	}
}
