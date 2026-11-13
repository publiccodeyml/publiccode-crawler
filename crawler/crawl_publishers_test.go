package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/italia/publiccode-crawler/v4/apiclient"
	"github.com/italia/publiccode-crawler/v4/common"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHost struct {
	repos int
	err   error
}

func (h fakeHost) Scan(repoURL url.URL, publisher common.Publisher, repos chan common.Repository) error {
	return h.List(repoURL, publisher, repos)
}

func (h fakeHost) List(groupURL url.URL, publisher common.Publisher, repos chan common.Repository) error {
	for i := range h.repos {
		repoURL := groupURL
		repoURL.Path += fmt.Sprintf("/repo-%d", i)

		repos <- common.Repository{
			Name:      publisher.Name + repoURL.Path,
			URL:       repoURL,
			Publisher: publisher,
		}
	}

	return h.err
}

func TestCrawlPublishers(t *testing.T) {
	const channelSize = 1000

	tests := []struct {
		name       string
		publishers []common.Publisher
		want       int
	}{
		{name: "no publishers"},
		{
			name: "more repositories than the channel holds",
			publishers: []common.Publisher{
				{Name: "large", Sources: []common.CodeHosting{source("large")}},
			},
			want: channelSize + 500,
		},
		{
			name: "scan error",
			publishers: []common.Publisher{
				{Name: "failing", Sources: []common.CodeHosting{source("failing"), source("small")}},
				{Name: "small", Sources: []common.CodeHosting{source("small")}},
			},
			want: 2 * 3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// An empty API base URL fails every lookup before any network
			// request, so the repository workers only log each repository.
			viper.Set("API_BASEURL", "")
			t.Cleanup(viper.Reset)

			hook := logtest.NewGlobal()
			t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })

			synctest.Test(t, func(t *testing.T) {
				crawler := &Crawler{
					DryRun:       true,
					Index:        "crawl-publishers",
					repositories: make(chan common.Repository, channelSize),
					hosts: map[string]vcsHost{
						"large":   {scanner: fakeHost{repos: channelSize + 500}, lister: fakeHost{repos: channelSize + 500}},
						"small":   {scanner: fakeHost{repos: 3}, lister: fakeHost{repos: 3}},
						"failing": {scanner: fakeHost{err: errors.New("scan failed")}, lister: fakeHost{err: errors.New("scan failed")}},
					},
					apiClient:          apiclient.NewClient(),
					startMetricsServer: func() {},
				}

				require.NoError(t, crawler.CrawlPublishers(test.publishers))
			})

			processed := map[string]int{}

			for _, entry := range hook.AllEntries() {
				if strings.Contains(entry.Message, "failed to GET software from API") {
					processed[strings.SplitN(entry.Message, "]", 2)[0]]++
				}
			}

			assert.Len(t, processed, test.want)

			for name, count := range processed {
				assert.Equal(t, 1, count, name)
			}
		})
	}
}

func source(driver string) common.CodeHosting {
	return common.CodeHosting{
		URL:    url.URL{Scheme: "https", Host: driver + ".example.test", Path: "/group"},
		Driver: driver,
		Group:  true,
	}
}
