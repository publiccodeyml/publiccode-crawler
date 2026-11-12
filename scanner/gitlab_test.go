package scanner

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitLabScannerUsesTargetInstanceAPI(t *testing.T) {
	requests := make([]string, 0, 4)
	expectedHost := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, expectedHost, r.Host)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/api/v4/"), r.URL.Path)
		requests = append(requests, r.URL.EscapedPath())

		switch r.URL.EscapedPath() {
		case "/api/v4/groups/example":
			_, _ = w.Write([]byte(`{"id": 1}`))
		case "/api/v4/groups/1/projects", "/api/v4/groups/1/descendant_groups":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v4/projects/example%2Fproject":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected GitLab API request: %s", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	expectedHost = strings.TrimPrefix(server.URL, "http://")

	instanceURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	scanner := NewGitLabScanner()
	repositories := make(chan common.Repository)

	groupURL := *instanceURL
	groupURL.Path = "/example"
	require.NoError(t, scanner.List(groupURL, common.Publisher{}, repositories))

	projectURL := *instanceURL
	projectURL.Path = "/example/project"
	require.NoError(t, scanner.Scan(projectURL, common.Publisher{}, repositories))

	assert.Equal(t, []string{
		"/api/v4/groups/example",
		"/api/v4/groups/1/projects",
		"/api/v4/groups/1/descendant_groups",
		"/api/v4/projects/example%2Fproject",
	}, requests)
}

func TestIsGitlabGroup(t *testing.T) {
	tests := []struct {
		rawURL string
		want   bool
	}{
		{"https://gitlab.com/mygroup", true},
		{"https://gitlab.com/mygroup/mysubgroup", true},
		{"https://mygitlab.example.com/mygroup", true},
		{"https://mygitlab.example.com/", false},
		{"https://mygitlab.example.com", false},
	}

	for _, tc := range tests {
		parsed, err := url.Parse(tc.rawURL)
		require.NoError(t, err)

		assert.Equal(t, tc.want, isGitlabGroup(*parsed), tc.rawURL)
	}
}

func TestGenerateGitlabRawURL(t *testing.T) {
	tests := []struct {
		baseURL       string
		defaultBranch string
		want          string
	}{
		{
			"https://gitlab.com/mygroup/myrepo",
			"main",
			"https://gitlab.com/mygroup/myrepo/raw/main/publiccode.yml",
		},
		{
			"https://mygitlab.example.com/org/suborg/repo",
			"develop",
			"https://mygitlab.example.com/org/suborg/repo/raw/develop/publiccode.yml",
		},
	}

	for _, tc := range tests {
		got, err := generateGitlabRawURL(tc.baseURL, tc.defaultBranch)

		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}
