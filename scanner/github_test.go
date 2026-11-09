package scanner

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v68/github"
	"github.com/italia/publiccode-crawler/v4/common"
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
