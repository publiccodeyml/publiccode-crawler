package git

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/italia/publiccode-crawler/v4/git/vitality"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustVitalityCachePath(t *testing.T, host, name string) string {
	t.Helper()

	path, err := vitalityCachePath(host, name)
	require.NoError(t, err)

	return path
}

func TestVitalityCachePathKeepsOwnerRepositoryLayout(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", "data")

	assert.Equal(t,
		filepath.Join("data", "repos", "example.test", "owner", "repository", "vitality.json"),
		mustVitalityCachePath(t, "example.test", "owner/repository"),
	)
}

func TestVitalityCachePathSeparatesNestedSiblings(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", "data")

	first := mustVitalityCachePath(t, "gitlab.example.test", "org/team/app1")
	second := mustVitalityCachePath(t, "gitlab.example.test", "org/team/app2")

	assert.Equal(t, filepath.Join("data", "repos", "gitlab.example.test", "org", "team", "app1", "vitality.json"), first)
	assert.Equal(t, filepath.Join("data", "repos", "gitlab.example.test", "org", "team", "app2", "vitality.json"), second)
}

func TestVitalityCachePathRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{
		"",
		"repository",
		"owner/",
		"/repository",
		"owner//repository",
		"owner/./repository",
		"owner/../repository",
		"owner/..",
		`owner\team/repository`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := vitalityCachePath("example.test", name)
			assert.Error(t, err)
		})
	}
}

func TestCloneRepositoryKeepsNestedSiblingCachesSeparate(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	const host = "gitlab.example.test"

	firstPath := filepath.Join(tmpDir, "app1")
	first, err := gogit.PlainInit(firstPath, false)
	require.NoError(t, err)
	commitToRepository(t, first, "first", fixedNow.AddDate(0, 0, -2))

	secondPath := filepath.Join(tmpDir, "app2")
	second, err := gogit.PlainInit(secondPath, false)
	require.NoError(t, err)
	commitToRepository(t, second, "first", fixedNow.AddDate(0, 0, -2))
	commitToRepository(t, second, "second", fixedNow.AddDate(0, 0, -1))

	require.NoError(t, CloneRepository(host, "org/team/app1", "file://"+firstPath, "test"))
	require.NoError(t, CloneRepository(host, "org/team/app2", "file://"+secondPath, "test"))

	assert.Equal(t, uint32(1), vitalityCommitCount(t, mustVitalityCachePath(t, host, "org/team/app1")))
	assert.Equal(t, uint32(2), vitalityCommitCount(t, mustVitalityCachePath(t, host, "org/team/app2")))
}

func TestCloneRepositoryRejectsMalformedName(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())

	err := CloneRepository("example.test", "repository", "https://example.test/repository.git", "test")

	assert.ErrorContains(t, err, "invalid repository name")
}

func TestCalculateRepoActivityReadsNestedSiblingCaches(t *testing.T) {
	t.Chdir("..")

	const host = "gitlab.example.test"

	dataDir := t.TempDir()
	viper.Set("DATADIR", dataDir)

	writeNestedCache := func(name string, authors []string) {
		entryAuthors := make([]uint16, len(authors))
		for idx := range authors {
			entryAuthors[idx] = uint16(idx)
		}

		data, err := vitality.Marshal(vitality.Cache{
			LastUpdated:      fixedNow,
			OldestCommitDate: fixedNow.AddDate(0, 0, -30),
			FirstEntryDate:   fixedNow,
			Authors:          authors,
			Entries: []vitality.DayEntry{
				{Delta: 0, Commits: uint32(len(authors)), Authors: entryAuthors},
			},
		})
		require.NoError(t, err)

		path := mustVitalityCachePath(t, host, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}

	writeNestedCache("org/team/app1", []string{"a@example.com"})
	writeNestedCache("org/team/app2", []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"})

	activity := func(name string) map[int]float64 {
		repo := common.Repository{Name: name}
		repo.URL.Host = host

		_, index, err := CalculateRepoActivity(repo, 1, fixedNow)
		require.NoError(t, err)

		return index
	}

	assert.NotEqual(t, activity("org/team/app1"), activity("org/team/app2"))
}

func TestCalculateRepoActivityRejectsMalformedName(t *testing.T) {
	repo := common.Repository{Name: "repository"}
	repo.URL.Host = "example.test"

	_, _, err := CalculateRepoActivity(repo, 1, fixedNow)

	assert.ErrorContains(t, err, "invalid repository name")
}

func TestCloneRepositoryWithLegacyCachePathCollision(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("DATADIR", t.TempDir())
	t.Chdir("..")

	const host = "gitlab.example.test"

	source := filepath.Join(t.TempDir(), "source")
	repository, err := gogit.PlainInit(source, false)
	require.NoError(t, err)
	commitToRepository(t, repository, "fresh", fixedNow)

	legacy, err := vitality.Marshal(vitality.Cache{
		LastUpdated:      fixedNow,
		OldestCommitDate: fixedNow.AddDate(-1, 0, 0),
		FirstEntryDate:   fixedNow,
		Authors:          []string{"legacy@example.test"},
		Entries:          []vitality.DayEntry{{Commits: 99, Authors: []uint16{0}}},
	})
	require.NoError(t, err)
	legacyPath := mustVitalityCachePath(t, host, "org/team")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(legacyPath, legacy, 0o600))

	paths := map[string]bool{legacyPath: true}
	for _, name := range []string{
		"org/team/app",
		"org/team/vitality.json",
		"org/team/vitality.json/app",
		"org/team/%76itality.json",
		"org/team/%2576itality.json",
		"org/team/app/vitality.json",
	} {
		path := mustVitalityCachePath(t, host, name)
		require.False(t, paths[path], "duplicate cache path for %s", name)
		paths[path] = true
		require.NoError(t, CloneRepository(host, name, "file://"+source, "test"))
		assert.Equal(t, uint32(1), vitalityCommitCount(t, path))
		cache := loadExistingCache(path)
		require.NotNil(t, cache)
		assert.Equal(t, []string{"test@example.test"}, cache.Authors)

		repo := common.Repository{Name: name}
		repo.URL.Host = host
		_, _, err := CalculateRepoActivity(repo, 1, fixedNow)
		require.NoError(t, err)
	}

	preserved, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	assert.Equal(t, legacy, preserved)
}
