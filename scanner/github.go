package scanner

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/italia/publiccode-crawler/v4/common"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// ErrGitHubTokenMissing is returned when a GitHub source is scanned
// without a GitHub API token.
var ErrGitHubTokenMissing = errors.New("please set GITHUB_TOKEN, it's needed to use the GitHub API")

type GitHubScanner struct {
	client *github.Client
	// Sources on other platforms don't need a GitHub token, so a missing one
	// only fails the GitHub sources.
	missingToken bool
}

// NewGitHubScanner returns a new GitHubScanner using the
// authentication token from the GITHUB_TOKEN environment variable.
func NewGitHubScanner() GitHubScanner {
	token := viper.GetString("GITHUB_TOKEN")
	if token == "" {
		return GitHubScanner{client: github.NewClient(nil), missingToken: true}
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)

	return GitHubScanner{client: github.NewClient(tc)}
}

// List scans a GitHub organization represented by url, associated to
// publisher and sends any repository containing a publiccode.yml to the repositories
// channel as a [common.Repository].
// It returns any error encountered if any, otherwise nil.
func (scanner GitHubScanner) List(
	url url.URL, publisher common.Publisher, repositories chan common.Repository,
) error {
	log.Debugf("GitHubScanner.List(%s)", url.String())

	if scanner.missingToken {
		return fmt.Errorf("can't list %s: %w", url.String(), ErrGitHubTokenMissing)
	}

	opt := &github.RepositoryListByOrgOptions{ListOptions: github.ListOptions{PerPage: 100}}

	splitted := strings.Split(strings.Trim(url.Path, "/"), "/")
	if len(splitted) != 1 {
		return fmt.Errorf("doesn't look like a GitHub org %s", url.String())
	}

	orgName := splitted[0]

	for {
	Retry:
		repos, resp, err := scanner.client.Repositories.ListByOrg(context.Background(), orgName, opt)

		if waitForRateLimit(resp, err) {
			goto Retry
		}

		if err != nil {
			// Try to list repos by user, for backwards compatibility.
			log.Warnf(
				"can't list repositories in %s (not an GitHub organization?): %s",
				url.String(), err.Error(),
			)

			var userOpt github.RepositoryListByUserOptions

			userOpt.ListOptions = opt.ListOptions

			repos, resp, err = scanner.client.Repositories.ListByUser(context.Background(), orgName, &userOpt)
			if err != nil {
				return fmt.Errorf("can't list repositories in %s (not an GitHub organization?): %w", url.String(), err)
			}

			log.Warnf(
				"%s is not a GitHub organization, listing repos as GitHub user. This will be removed in the future",
				url.String(),
			)
		}

		scanner.scanRepositories(repos, publisher, repositories)

		if resp.NextPage == 0 {
			break
		}

		opt.Page = resp.NextPage
	}

	return nil
}

// Scan scans a GitHub repository represented by url, associated to
// publisher and, if it contains a publiccode.yml, sends it as a [common.Repository]
// repositories channel.
// It returns any error encountered if any, otherwise nil.
func (scanner GitHubScanner) Scan(
	url url.URL, publisher common.Publisher, repositories chan common.Repository,
) error {
	log.Debugf("GitHubScanner.Scan(%s)", url.String())

	if scanner.missingToken {
		return fmt.Errorf("can't scan %s: %w", url.String(), ErrGitHubTokenMissing)
	}

	splitted := strings.Split(strings.TrimSuffix(strings.Trim(url.Path, "/"), ".git"), "/")
	if len(splitted) != 2 {
		return fmt.Errorf("doesn't look like a GitHub repo %s", url.String())
	}

	orgName := splitted[0]
	repoName := splitted[1]

Retry:
	repo, resp, err := scanner.client.Repositories.Get(context.Background(), orgName, repoName)

	if waitForRateLimit(resp, err) {
		goto Retry
	}

	if err != nil {
		return fmt.Errorf("can't get repo %s: %w", url.String(), err)
	}

	return scanner.scanRepository(repo, url, publisher, repositories)
}

// scanRepository sends repo to repositories if it contains a publiccode.yml.
// It takes the repository as returned by the GitHub API, so a listing doesn't
// need a second request for each of its repositories.
func (scanner GitHubScanner) scanRepository(
	repo *github.Repository, url url.URL, publisher common.Publisher, repositories chan common.Repository,
) error {
	if repo.GetPrivate() || repo.GetArchived() {
		return fmt.Errorf("skipping private or archived repo %s", repo.GetFullName())
	}

	orgName, repoName, ok := strings.Cut(repo.GetFullName(), "/")
	if !ok {
		return fmt.Errorf("doesn't look like a GitHub repo name %q", repo.GetFullName())
	}

Retry:
	file, _, resp, err := scanner.client.Repositories.GetContents(
		context.Background(), orgName, repoName, "publiccode.yml", nil,
	)

	if waitForRateLimit(resp, err) {
		goto Retry
	}

	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return ErrPubliccodeNotFound
		}

		return fmt.Errorf("[%s]: failed to get publiccode.yml: %w", repo.GetFullName(), err)
	}

	// GetContents returns no file content and no error when the path is a directory.
	if file == nil || file.DownloadURL == nil {
		return fmt.Errorf("[%s]: failed to get publiccode.yml: not a regular file?", repo.GetFullName())
	}

	canonicalURL, err := url.Parse(repo.GetCloneURL())
	if err != nil {
		return fmt.Errorf("failed to get canonical repo URL for %s: %w", url.String(), err)
	}

	repositories <- common.Repository{
		Name:         repo.GetFullName(),
		FileRawURL:   *file.DownloadURL,
		URL:          url,
		CanonicalURL: *canonicalURL,
		GitBranch:    repo.GetDefaultBranch(),
		Publisher:    publisher,
		Headers:      make(map[string]string),
	}

	return nil
}

// waitForRateLimit sleeps until GitHub accepts requests again when err is a
// rate limit error, and reports whether the request should be retried.
func waitForRateLimit(resp *github.Response, err error) bool {
	var rateLimitError *github.RateLimitError
	if errors.As(err, &rateLimitError) {
		log.Infof("GitHub rate limit hit, sleeping until %s", resp.Rate.Reset.Time.String())
		time.Sleep(time.Until(resp.Rate.Reset.Time))

		return true
	}

	var abuseRateLimitError *github.AbuseRateLimitError
	if errors.As(err, &abuseRateLimitError) {
		secondaryRateLimit(abuseRateLimitError)

		return true
	}

	return false
}

func secondaryRateLimit(err *github.AbuseRateLimitError) {
	var duration time.Duration
	if err.RetryAfter != nil {
		duration = *err.RetryAfter
	} else {
		duration = time.Duration(rand.Intn(100)) * time.Second //nolint:gosec
	}

	log.Infof("GitHub secondary rate limit hit, for %s", duration)
	time.Sleep(duration)
}

func (scanner GitHubScanner) scanRepositories(
	repos []*github.Repository, publisher common.Publisher, repositories chan common.Repository,
) {
	for _, repo := range repos {
		repoURL, err := url.Parse(*repo.HTMLURL)
		if err != nil {
			log.Errorf("can't parse URL %s: %s", *repo.URL, err.Error())

			continue
		}

		if err = scanner.scanRepository(repo, *repoURL, publisher, repositories); err != nil {
			if errors.Is(err, ErrPubliccodeNotFound) {
				log.Warnf("can't scan repository %s: %s", repoURL.String(), err.Error())
			} else {
				log.Errorf("can't scan repository %s: %s", repoURL.String(), err.Error())
			}

			continue
		}
	}
}
