package scanner

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v68/github"
	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newGitHubScannerForTransport(transport http.RoundTripper) GitHubScanner {
	return GitHubScanner{client: github.NewClient(&http.Client{Transport: transport})}
}

func githubRepositoryResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"private": false,
			"archived": false,
			"full_name": "org/repository",
			"clone_url": "https://github.com/org/repository.git",
			"default_branch": "main"
		}`)),
	}
}

func TestGitHubScannerScanReturnsContentTransportError(t *testing.T) {
	transportError := errors.New("content transport failed")
	scanner := newGitHubScannerForTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/org/repository":
			return githubRepositoryResponse(), nil
		case "/repos/org/repository/contents/publiccode.yml":
			return nil, transportError
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	}))

	repositoryURL, err := url.Parse("https://github.com/org/repository")
	require.NoError(t, err)

	err = scanner.Scan(*repositoryURL, common.Publisher{}, make(chan common.Repository, 1))

	require.Error(t, err)
	assert.ErrorIs(t, err, transportError)
}

func TestGitHubScannerScanKeepsNotFoundContentError(t *testing.T) {
	scanner := newGitHubScannerForTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/org/repository":
			return githubRepositoryResponse(), nil
		case "/repos/org/repository/contents/publiccode.yml":
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)),
			}, nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	}))

	repositoryURL, err := url.Parse("https://github.com/org/repository")
	require.NoError(t, err)

	err = scanner.Scan(*repositoryURL, common.Publisher{}, make(chan common.Repository, 1))

	assert.ErrorIs(t, err, ErrPubliccodeNotFound)
}

func githubContentsResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestGitHubScannerScanRejectsPubliccodeDirectory(t *testing.T) {
	scanner := newGitHubScannerForTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/org/repository":
			return githubRepositoryResponse(), nil
		case "/repos/org/repository/contents/publiccode.yml":
			return githubContentsResponse(`[{
				"type": "file",
				"name": "README.md",
				"path": "publiccode.yml/README.md",
				"download_url": "https://raw.githubusercontent.com/org/repository/main/publiccode.yml/README.md"
			}]`), nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	}))

	repositoryURL, err := url.Parse("https://github.com/org/repository")
	require.NoError(t, err)

	repositories := make(chan common.Repository, 1)
	err = scanner.Scan(*repositoryURL, common.Publisher{}, repositories)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPubliccodeNotFound)
	assert.Empty(t, repositories)
}

func TestGitHubScannerScanSendsRegularPubliccodeFile(t *testing.T) {
	const downloadURL = "https://raw.githubusercontent.com/org/repository/main/publiccode.yml"

	scanner := newGitHubScannerForTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/org/repository":
			return githubRepositoryResponse(), nil
		case "/repos/org/repository/contents/publiccode.yml":
			return githubContentsResponse(`{
				"type": "file",
				"name": "publiccode.yml",
				"path": "publiccode.yml",
				"download_url": "` + downloadURL + `"
			}`), nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	}))

	repositoryURL, err := url.Parse("https://github.com/org/repository")
	require.NoError(t, err)

	repositories := make(chan common.Repository, 1)
	err = scanner.Scan(*repositoryURL, common.Publisher{}, repositories)

	require.NoError(t, err)
	require.Len(t, repositories, 1)

	repository := <-repositories
	assert.Equal(t, "org/repository", repository.Name)
	assert.Equal(t, downloadURL, repository.FileRawURL)
	assert.Equal(t, "https://github.com/org/repository.git", repository.CanonicalURL.String())
	assert.Equal(t, "main", repository.GitBranch)
}

func TestGitHubScannerListPaginates(t *testing.T) {
	for _, accountType := range []string{"user", "organization"} {
		t.Run(accountType, func(t *testing.T) {
			var pages []string
			var organizationRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/orgs/account/repos" {
					organizationRequests++
					if accountType == "user" {
						http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
						return
					}
				}
				switch r.URL.Path {
				case "/orgs/account/repos", "/users/account/repos":
					page := r.URL.Query().Get("page")
					pages = append(pages, page)
					if len(pages) > 2 {
						http.Error(w, `{"message":"repeated page"}`, http.StatusInternalServerError)
						return
					}
					var name string
					switch page {
					case "":
						name = "first"
						w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=2>; rel="next"`, r.Host, r.URL.Path))
					case "2":
						name = "second"
					default:
						http.Error(w, `{"message":"unexpected page"}`, http.StatusBadRequest)
						return
					}
					fmt.Fprintf(w, `[{"html_url":"https://github.com/account/%s"}]`, name)
				case "/repos/account/first", "/repos/account/second":
					name := strings.TrimPrefix(r.URL.Path, "/repos/")
					fmt.Fprintf(w, `{"private":false,"archived":false,"full_name":%q,"clone_url":%q,"default_branch":"main"}`,
						name, "https://github.com/"+name+".git")
				case "/repos/account/first/contents/publiccode.yml", "/repos/account/second/contents/publiccode.yml":
					fmt.Fprint(w, `{"type":"file","download_url":"https://example.com/publiccode.yml"}`)
				default:
					http.Error(w, `{"message":"unexpected path"}`, http.StatusNotFound)
				}
			}))
			defer server.Close()

			client := github.NewClient(server.Client())
			baseURL, err := url.Parse(server.URL + "/")
			require.NoError(t, err)
			client.BaseURL = baseURL
			scanner := GitHubScanner{client: client}
			accountURL, err := url.Parse("https://github.com/account")
			require.NoError(t, err)
			publisher := common.Publisher{ID: "publisher"}
			repositories := make(chan common.Repository, 4)

			err = scanner.List(*accountURL, publisher, repositories)

			require.NoError(t, err)
			assert.Equal(t, []string{"", "2"}, pages)
			assert.Equal(t, 2, organizationRequests)
			require.Len(t, repositories, 2)
			for _, name := range []string{"account/first", "account/second"} {
				repository := <-repositories
				assert.Equal(t, name, repository.Name)
				assert.Equal(t, publisher, repository.Publisher)
			}
		})
	}
}

func TestGitHubScannerWithoutTokenFailsBeforeRequests(t *testing.T) {
	viper.Set("GITHUB_TOKEN", "")
	t.Cleanup(viper.Reset)

	scanner := NewGitHubScanner()
	scanner.client = github.NewClient(&http.Client{Transport: roundTripperFunc(
		func(request *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request to %s", request.URL)

			return nil, nil
		},
	)})

	orgURL, err := url.Parse("https://github.com/org")
	require.NoError(t, err)

	repositoryURL, err := url.Parse("https://github.com/org/repository")
	require.NoError(t, err)

	err = scanner.List(*orgURL, common.Publisher{}, make(chan common.Repository, 1))
	require.ErrorIs(t, err, ErrGitHubTokenMissing)

	err = scanner.Scan(*repositoryURL, common.Publisher{}, make(chan common.Repository, 1))
	require.ErrorIs(t, err, ErrGitHubTokenMissing)
}
