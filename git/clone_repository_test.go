package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
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

	require.NoError(t, CloneRepository(host, name, repositoryPath, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))

	cachePath := vitalityCachePath(host, "owner", "repository")
	assert.Equal(t, uint32(1), vitalityCommitCount(t, cachePath))

	commitToRepository(t, repository, "second", fixedNow)

	require.NoError(t, CloneRepository(host, name, repositoryPath, "test"))
	assert.Empty(t, vitalityTempDirectories(t, tmpDir))
	assert.Equal(t, uint32(2), vitalityCommitCount(t, cachePath))
}

func commitToRepository(t *testing.T, repository *gogit.Repository, contents string, when time.Time) {
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
