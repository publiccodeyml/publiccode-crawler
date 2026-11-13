package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/italia/publiccode-crawler/v4/git/vitality"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneRepositoryUpdatesVitalityCache(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	repositoryPath := filepath.Join(tmpDir, "source")
	repository, err := gogit.PlainInit(repositoryPath, false)
	require.NoError(t, err)

	commitToRepository(t, repository, "first", fixedNow.AddDate(0, 0, -1))

	const (
		host = "example.test"
		name = "owner/repository"
	)

	repositoryURL := "file://" + repositoryPath
	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))

	cachePath := mustVitalityCachePath(t, host, "owner/repository")
	assert.Equal(t, uint32(1), vitalityCommitCount(t, cachePath))
	setVitalityCacheLastUpdated(t, cachePath, fixedNow)

	commitToRepository(t, repository, "second", fixedNow.AddDate(0, 0, 1))

	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))
	assert.Equal(t, uint32(2), vitalityCommitCount(t, cachePath))
}

func TestCloneRepositoryKeepsCacheWhenNoNewCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	repositoryPath := filepath.Join(tmpDir, "source")
	repository, err := gogit.PlainInit(repositoryPath, false)
	require.NoError(t, err)

	commitToRepository(t, repository, "first", fixedNow)

	const (
		host = "example.test"
		name = "owner/repository"
	)

	repositoryURL := "file://" + repositoryPath
	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))
	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))

	cachePath := mustVitalityCachePath(t, host, "owner/repository")
	assert.Equal(t, uint32(1), vitalityCommitCount(t, cachePath))
}

func TestCloneRepositoryKeepsCommitsBehindShallowMerge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	repositoryPath := filepath.Join(tmpDir, "source")
	repository, err := gogit.PlainInit(repositoryPath, false)
	require.NoError(t, err)

	commitToRepository(t, repository, "original", fixedNow.AddDate(0, 0, -1))

	const (
		host = "example.test"
		name = "owner/repository"
	)

	repositoryURL := "file://" + repositoryPath
	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))

	cachePath := mustVitalityCachePath(t, host, "owner/repository")
	setVitalityCacheLastUpdated(t, cachePath, fixedNow)

	worktree, err := repository.Worktree()
	require.NoError(t, err)
	original, err := repository.Head()
	require.NoError(t, err)
	require.NoError(t, repository.Storer.SetReference(plumbing.NewHashReference("refs/heads/side", original.Hash())))

	commitToRepository(t, repository, "main", fixedNow.AddDate(0, 0, 1))
	main, err := repository.Head()
	require.NoError(t, err)

	require.NoError(t, worktree.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/side"}))
	commitToRepository(t, repository, "side one", fixedNow.AddDate(0, 0, 1))
	commitToRepository(t, repository, "side two", fixedNow.AddDate(0, 0, 1))
	side, err := repository.Head()
	require.NoError(t, err)

	require.NoError(t, worktree.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/master"}))
	commitToRepositoryWithParents(t, repository, "merge", fixedNow.AddDate(0, 0, 1), main.Hash(), side.Hash())

	require.NoError(t, CloneRepository(host, name, repositoryURL, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))
	assert.Equal(t, uint32(5), vitalityCommitCount(t, cachePath))
}

func commitToRepository(t *testing.T, repository *gogit.Repository, contents string, when time.Time) {
	commitToRepositoryWithParents(t, repository, contents, when)
}

func commitToRepositoryWithParents(
	t *testing.T, repository *gogit.Repository, contents string, when time.Time, parents ...plumbing.Hash,
) {
	t.Helper()

	worktree, err := repository.Worktree()
	require.NoError(t, err)

	filePath := filepath.Join(worktree.Filesystem.Root(), "activity.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(contents), 0o600))
	_, err = worktree.Add("activity.txt")
	require.NoError(t, err)

	_, err = worktree.Commit(contents, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.test",
			When:  when,
		},
		Parents: parents,
	})
	require.NoError(t, err)
}

func vitalityTempDirectories(t *testing.T, tmpDir string) []string {
	t.Helper()

	directories, err := filepath.Glob(filepath.Join(tmpDir, "vitality-*"))
	require.NoError(t, err)

	return directories
}

func vitalityCommitCount(t *testing.T, cachePath string) uint32 {
	t.Helper()

	data, err := os.ReadFile(cachePath)
	require.NoError(t, err)

	cache, err := vitality.Unmarshal(data)
	require.NoError(t, err)

	var count uint32
	for _, entry := range cache.Entries {
		count += entry.Commits
	}

	return count
}

func setVitalityCacheLastUpdated(t *testing.T, cachePath string, updated time.Time) {
	t.Helper()

	data, err := os.ReadFile(cachePath)
	require.NoError(t, err)

	cache, err := vitality.Unmarshal(data)
	require.NoError(t, err)
	cache.LastUpdated = updated

	data, err = vitality.Marshal(cache)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cachePath, data, 0o600))
}

func TestCloneRepositoryCountsReleaseTags(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	repositoryPath := filepath.Join(t.TempDir(), "source")
	repository, err := gogit.PlainInit(repositoryPath, false)
	require.NoError(t, err)

	commitToRepository(t, repository, "first", fixedNow.AddDate(0, 0, -2))
	first, err := repository.Head()
	require.NoError(t, err)
	_, err = repository.CreateTag("v1.0.0", first.Hash(), nil)
	require.NoError(t, err)
	annotated, err := repository.CreateTag("v1.0.1", first.Hash(), &gogit.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Release Author", Email: "release@example.test", When: fixedNow},
		Message: "First annotated release",
	})
	require.NoError(t, err)

	_, err = repository.CreateTag("v1.0.2", annotated.Hash(), &gogit.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Release Author", Email: "release@example.test", When: fixedNow},
		Message: "Nested annotated release",
	})
	require.NoError(t, err)

	commitToRepository(t, repository, "second", fixedNow.AddDate(0, 0, -1))
	second, err := repository.Head()
	require.NoError(t, err)
	_, err = repository.CreateTag("v2.0.0", second.Hash(), &gogit.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Release Author", Email: "release@example.test", When: fixedNow},
		Message: "Second annotated release",
	})
	require.NoError(t, err)

	tags, err := extractAllTagsCommit(repository)
	require.NoError(t, err)
	require.Len(t, tags, 4)
	hashes := make([]plumbing.Hash, 0, len(tags))
	for _, tag := range tags {
		hashes = append(hashes, tag.Hash)
	}
	assert.ElementsMatch(t, []plumbing.Hash{first.Hash(), first.Hash(), first.Hash(), second.Hash()}, hashes)

	require.NoError(t, CloneRepository("example.test", "owner/releases", "file://"+repositoryPath, "test"))
	data, err := os.ReadFile(mustVitalityCachePath(t, "example.test", "owner/releases"))
	require.NoError(t, err)
	cache, err := vitality.Unmarshal(data)
	require.NoError(t, err)
	assert.True(t, cache.FirstEntryDate.Equal(fixedNow.AddDate(0, 0, -2).Truncate(24*time.Hour)))
	assert.Equal(t, []vitality.TagEntry{{Delta: 0, Count: 3}, {Delta: 1, Count: 1}}, cache.Tags)
}
