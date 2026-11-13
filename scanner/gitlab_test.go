package scanner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
		case "/api/v4/groups/1/projects", "/api/v4/groups/1/subgroups":
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
		"/api/v4/groups/1/subgroups",
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

func TestGitLabScannerListsNestedGroupsOnce(t *testing.T) {
	for _, groupPath := range []string{"/example", "/"} {
		t.Run(groupPath, func(t *testing.T) {
			requests := make(map[string]int)
			responses := map[string]string{
				"/api/v4/groups/example?page=":      `{"id":1}`,
				"/api/v4/groups?page=1":             `[{"id":1}]`,
				"/api/v4/groups/1/subgroups?page=1": `[{"id":2}]`,
				"/api/v4/groups/1/subgroups?page=2": `[{"id":4}]`,
				"/api/v4/groups/2/subgroups?page=1": `[{"id":3}]`,
				"/api/v4/groups/3/subgroups?page=1": `[]`,
				"/api/v4/groups/4/subgroups?page=1": `[]`,
			}
			for _, id := range []int{1, 2, 3, 4} {
				responses[fmt.Sprintf("/api/v4/groups/%d/projects?page=1", id)] = fmt.Sprintf(
					`[{"visibility":"public","path_with_namespace":"group%d/project","default_branch":"main","web_url":"https://gitlab.example/group%d/project","http_url_to_repo":"https://gitlab.example/group%d/project.git"}]`, id, id, id)
			}
			responses["/api/v4/groups/1/projects?page=2"] = `[{"visibility":"public","path_with_namespace":"example/extra","default_branch":"main","web_url":"https://gitlab.example/example/extra","http_url_to_repo":"https://gitlab.example/example/extra.git"}]`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.URL.Path + "?page=" + r.URL.Query().Get("page")
				requests[key]++
				body, ok := responses[key]
				if !ok {
					t.Errorf("unexpected GitLab API request: %s", key)
					http.NotFound(w, r)
					return
				}
				if key == "/api/v4/groups/1/subgroups?page=1" || key == "/api/v4/groups/1/projects?page=1" {
					w.Header().Set("X-Next-Page", "2")
				}
				if r.URL.Path == "/api/v4/groups" {
					assert.Equal(t, "true", r.URL.Query().Get("top_level_only"))
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			groupURL, err := url.Parse(server.URL + groupPath)
			require.NoError(t, err)
			repositories := make(chan common.Repository, 16)
			require.NoError(t, NewGitLabScanner().List(*groupURL, common.Publisher{}, repositories))
			close(repositories)

			names := make([]string, 0, len(repositories))
			for repository := range repositories {
				names = append(names, repository.Name)
			}
			assert.ElementsMatch(t, []string{"group1/project", "example/extra", "group2/project", "group3/project", "group4/project"}, names)
			for key := range responses {
				if (groupPath == "/" && strings.Contains(key, "/groups/example")) || (groupPath != "/" && key == "/api/v4/groups?page=1") {
					continue
				}
				assert.Equal(t, 1, requests[key], key)
			}
		})
	}
}

func TestGitLabScannerSkipsIneligibleProjects(t *testing.T) {
	for _, tc := range []struct {
		name       string
		visibility string
		archived   bool
		branch     string
		want       int
	}{
		{"public", "public", false, "main", 1},
		{"archived", "public", true, "main", 0},
		{"private", "private", false, "main", 0},
		{"internal", "internal", false, "main", 0},
		{"empty", "public", false, "", 0},
	} {
		for _, method := range []string{"List", "Scan", "ScanWithoutToken", "ScanUserWithoutToken"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				t.Setenv("GITLAB_TOKEN", "test-token")
				namespace := `{"id":1,"kind":"group","full_path":"example"}`
				if method == "ScanUserWithoutToken" {
					namespace = `{"id":5,"kind":"user","full_path":"example"}`
				}
				project := fmt.Sprintf(`{"id":7,"path":"project","namespace":%s,"visibility":%q,"archived":%t,"default_branch":%q,"path_with_namespace":"example/project","web_url":"https://gitlab.example/example/project","http_url_to_repo":"https://gitlab.example/example/project.git"}`, namespace, tc.visibility, tc.archived, tc.branch)
				single := project
				if strings.HasSuffix(method, "WithoutToken") {
					t.Setenv("GITLAB_TOKEN", "")
					// GitLab omits archived from unauthenticated single project responses.
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(project), &fields))
					delete(fields, "archived")
					body, err := json.Marshal(fields)
					require.NoError(t, err)
					single = string(body)
				}
				archivedListing := func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "project", r.URL.Query().Get("search"))
					if tc.archived {
						_, _ = fmt.Fprintf(w, `[{"id":3},%s]`, project)
					} else {
						_, _ = w.Write([]byte(`[{"id":3}]`))
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, os.Getenv("GITLAB_TOKEN"), r.Header.Get("PRIVATE-TOKEN"))
					archivedOnly := r.URL.Query().Get("archived") == "true"
					switch r.URL.EscapedPath() {
					case "/api/v4/groups/example":
						_, _ = w.Write([]byte(`{"id":1}`))
					case "/api/v4/groups/1/projects":
						if archivedOnly {
							archivedListing(w, r)
						} else {
							_, _ = fmt.Fprintf(w, "[%s]", project)
						}
					case "/api/v4/users/example/projects":
						assert.True(t, archivedOnly)
						archivedListing(w, r)
					case "/api/v4/groups/1/subgroups":
						_, _ = w.Write([]byte(`[]`))
					case "/api/v4/projects/example%2Fproject":
						_, _ = w.Write([]byte(single))
					default:
						t.Errorf("unexpected GitLab API request: %s", r.URL.EscapedPath())
						http.NotFound(w, r)
					}
				}))
				defer server.Close()

				target, err := url.Parse(server.URL + "/example")
				require.NoError(t, err)
				repositories := make(chan common.Repository, 2)
				scanner := NewGitLabScanner()
				if method == "List" {
					err = scanner.List(*target, common.Publisher{}, repositories)
				} else {
					target.Path = "/example/project"
					err = scanner.Scan(*target, common.Publisher{}, repositories)
				}
				require.NoError(t, err)
				require.Len(t, repositories, tc.want)
				if tc.want > 0 {
					assert.Equal(t, "example/project", (<-repositories).Name)
				}
			})
		}
	}
}
