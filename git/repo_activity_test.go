package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/italia/publiccode-crawler/v4/git/vitality"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow is a fixed point in time used to make all tests deterministic.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestMeanActivity(t *testing.T) {
	assert.Equal(t, float64(20), meanActivity(map[int]float64{0: 10, 1: 20, 2: 30}))
}

func TestMeanActivitySingle(t *testing.T) {
	assert.Equal(t, float64(42), meanActivity(map[int]float64{0: 42}))
}

func TestCloneRepositoryTimeout(t *testing.T) {
	dir := t.TempDir()
	gitPath := filepath.Join(dir, "git")
	callsPath := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nexec \"$GO_TEST_BINARY\" -test.run='^TestCloneRepositoryHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(gitPath, []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("GO_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_CLONE_HELPER", "1")
	t.Setenv("GO_CLONE_HELPER_CALLS", callsPath)
	t.Setenv("GO_CLONE_HELPER_HANG_ON", "first")
	viper.Set("DATADIR", t.TempDir())

	previousTimeout := cloneTimeout
	cloneTimeout = 100 * time.Millisecond
	t.Cleanup(func() { cloneTimeout = previousTimeout })

	started := time.Now()
	err := CloneRepository("example.com", "owner/repository", "https://example.com/repository.git", "test")

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "https://example.com/repository.git")
	assert.Less(t, time.Since(started), 3*time.Second)
	assert.FileExists(t, callsPath)
	assert.Equal(t, "1", string(mustReadFile(t, callsPath)))
}

func TestCloneRepositoryFallbackTimeout(t *testing.T) {
	dir := t.TempDir()
	gitPath := filepath.Join(dir, "git")
	callsPath := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nexec \"$GO_TEST_BINARY\" -test.run='^TestCloneRepositoryHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(gitPath, []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("GO_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_CLONE_HELPER", "1")
	t.Setenv("GO_CLONE_HELPER_CALLS", callsPath)
	t.Setenv("GO_CLONE_HELPER_HANG_ON", "fallback")

	dataDir := t.TempDir()
	viper.Set("DATADIR", dataDir)
	cache, err := vitality.Marshal(vitality.Cache{LastUpdated: time.Now()})
	require.NoError(t, err)
	cachePath := mustVitalityCachePath(t, "example.com", "owner/repository")
	require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
	require.NoError(t, os.WriteFile(cachePath, cache, 0o600))

	previousTimeout := cloneTimeout
	cloneTimeout = 100 * time.Millisecond
	t.Cleanup(func() { cloneTimeout = previousTimeout })

	started := time.Now()
	err = CloneRepository("example.com", "owner/repository", "https://example.com/repository.git", "test")

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "https://example.com/repository.git")
	assert.Less(t, time.Since(started), 3*time.Second)
	assert.FileExists(t, callsPath)
	assert.Equal(t, "2", string(mustReadFile(t, callsPath)))
}

func TestCloneRepositoryHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLONE_HELPER") != "1" {
		return
	}
	if os.Getenv("GO_WANT_CLONE_HELPER_CHILD") == "1" {
		time.Sleep(5 * time.Second)
		return
	}

	callsPath := os.Getenv("GO_CLONE_HELPER_CALLS")
	calls, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		os.Exit(1)
	}

	if len(calls) == 0 {
		require.NoError(t, os.WriteFile(callsPath, []byte("1"), 0o600))
		if os.Getenv("GO_CLONE_HELPER_HANG_ON") == "first" {
			hangCloneHelper(t)
		}

		for _, arg := range os.Args {
			if strings.HasPrefix(arg, "--shallow-since=") {
				os.Exit(128)
			}
		}

		os.Exit(1)
	}

	require.NoError(t, os.WriteFile(callsPath, []byte("2"), 0o600))
	if os.Getenv("GO_CLONE_HELPER_HANG_ON") == "fallback" {
		hangCloneHelper(t)
	}

	os.Exit(1)
}

func hangCloneHelper(t *testing.T) {
	t.Helper()

	child := exec.Command(os.Args[0], "-test.run=^TestCloneRepositoryHelperProcess$", "--")
	child.Env = append(os.Environ(), "GO_WANT_CLONE_HELPER_CHILD=1")
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	require.NoError(t, child.Start())
	select {}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return data
}
