package apiclient

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) APIClient {
	t.Helper()

	return newTimeoutTestClient(t, apiAttemptTimeout, apiOperationTimeout, handler)
}

func newTimeoutTestClient(
	t *testing.T, attemptTimeout, operationTimeout time.Duration, handler http.HandlerFunc,
) APIClient {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	viper.Set("API_BASEURL", server.URL)
	viper.Set("API_BEARER_TOKEN", "token")

	client := newClient(attemptTimeout, operationTimeout)
	retryClient := client.retryableClient.Transport.(*retryablehttp.RoundTripper).Client
	retryClient.RetryWaitMin = 0
	retryClient.RetryWaitMax = 0

	return client
}

func patchBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()

	bytes, err := io.ReadAll(r.Body)
	assert.NoError(t, err)

	var fields map[string]any
	assert.NoError(t, json.Unmarshal(bytes, &fields))

	return fields
}

func TestGetSoftwareByURL_includesInactive(t *testing.T) {
	var query string

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery

		_, _ = w.Write([]byte(`{"data": [{"id": "id1", "active": false}], "links": {}}`))
	})

	software, err := client.GetSoftwareByURL("https://example.org/repo.git")

	assert.NoError(t, err)
	assert.Contains(t, query, "all=true")
	assert.Equal(t, "id1", software.ID)
	assert.False(t, software.Active)
}

func TestGetCatalogSoftwareByURL_includesInactive(t *testing.T) {
	var query string

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery

		_, _ = w.Write([]byte(`{"data": [{"id": "id1", "active": false}], "links": {}}`))
	})

	software, err := client.GetCatalogSoftwareByURL("catalog1", "https://example.org/repo.git")

	assert.NoError(t, err)
	assert.Contains(t, query, "all=true")
	assert.Equal(t, "id1", software.ID)
}

func TestGetSoftwareByURL_rejectsUnsuccessfulResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	software, err := client.GetSoftwareByURL("https://example.org/repo.git")

	assert.Nil(t, software)
	assert.ErrorContains(t, err, "HTTP status 404")
}

func TestGetCatalogSoftwareByURL_rejectsUnsuccessfulResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	software, err := client.GetCatalogSoftwareByURL("catalog1", "https://example.org/repo.git")

	assert.Nil(t, software)
	assert.ErrorContains(t, err, "HTTP status 404")
}

func TestGetSoftware_rejectsUnsuccessfulResponse(t *testing.T) {
	attempts := 0

	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++

		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	})

	software, err := client.GetSoftware("id1")

	assert.Nil(t, software)
	assert.ErrorContains(t, err, "HTTP status 503")
	assert.Equal(t, client.retryableClient.Transport.(*retryablehttp.RoundTripper).Client.RetryMax+1, attempts)
}

func TestPatchSoftware_sendsActive(t *testing.T) {
	var fields map[string]any

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields = patchBody(t, r)

		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PatchSoftware("id1", "https://example.org/repo.git", nil, ptr("yml"), true)

	assert.NoError(t, err)
	assert.Equal(t, true, fields["active"])
}

func TestPatchSoftware_omitsMissingFile(t *testing.T) {
	var fields map[string]any

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields = patchBody(t, r)

		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PatchSoftware("id1", "https://example.org/repo.git", nil, nil, true)

	assert.NoError(t, err)
	assert.NotContains(t, fields, "publiccodeYml")
	assert.Equal(t, "https://example.org/repo.git", fields["url"])
}

func TestPatchCatalogSoftware_omitsMissingFile(t *testing.T) {
	var fields map[string]any

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields = patchBody(t, r)

		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PatchCatalogSoftware("catalog1", "id1", "https://example.org/repo.git", nil, nil, true)

	assert.NoError(t, err)
	assert.NotContains(t, fields, "publiccodeYml")
	assert.Equal(t, "https://example.org/repo.git", fields["url"])
}

func ptr(value string) *string {
	return &value
}

func TestPatchCatalogSoftware_sendsActive(t *testing.T) {
	var fields map[string]any

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields = patchBody(t, r)

		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PatchCatalogSoftware("catalog1", "id1", "https://example.org/repo.git", nil, ptr("yml"), true)

	assert.NoError(t, err)
	assert.Equal(t, true, fields["active"])
}

const (
	testAttemptTimeout   = 50 * time.Millisecond
	testOperationTimeout = time.Second
)

// stall blocks a handler until the client gives up, or until the test
// ends so that the server can close.
func stall(t *testing.T) func(*http.Request) {
	t.Helper()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	return func(r *http.Request) {
		// The server notices a closed connection only after the request
		// body has been read.
		_, _ = io.Copy(io.Discard, r.Body)

		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
}

func stallBody(w http.ResponseWriter, r *http.Request, wait func(*http.Request)) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"id": "id1"`))
	w.(http.Flusher).Flush()
	wait(r)
}

func TestGetSoftware_retriesStalledHeaders(t *testing.T) {
	var attempts atomic.Int32

	wait := stall(t)
	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(_ http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			wait(r)
		})

	start := time.Now()
	software, err := client.GetSoftware("id1")

	assert.Nil(t, software)
	var timeoutErr net.Error
	if assert.ErrorAs(t, err, &timeoutErr) {
		assert.True(t, timeoutErr.Timeout())
	}
	assert.Less(t, time.Since(start), testOperationTimeout)
	assert.EqualValues(t, client.retryableClient.Transport.(*retryablehttp.RoundTripper).Client.RetryMax+1,
		attempts.Load())
}

func TestGetSoftware_boundsStalledBody(t *testing.T) {
	wait := stall(t)
	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(w http.ResponseWriter, r *http.Request) {
			stallBody(w, r, wait)
		})

	start := time.Now()
	software, err := client.GetSoftware("id1")

	assert.Nil(t, software)
	assert.ErrorContains(t, err, "can't parse GET /software/id1 response")
	assert.Less(t, time.Since(start), testOperationTimeout)
}

func TestPostSoftwareLog_boundsStalledHeaders(t *testing.T) {
	wait := stall(t)
	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(_ http.ResponseWriter, r *http.Request) {
			wait(r)
		})

	start := time.Now()
	err := client.PostSoftwareLog("id1", "message")

	assert.ErrorContains(t, err, "can't create software log")
	assert.Less(t, time.Since(start), testOperationTimeout)
}

func TestPostSoftware_boundsStalledBody(t *testing.T) {
	wait := stall(t)
	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(w http.ResponseWriter, r *http.Request) {
			stallBody(w, r, wait)
		})

	start := time.Now()
	software, err := client.PostSoftware("https://example.org/repo.git", nil, "yml", true)

	assert.Nil(t, software)
	assert.ErrorContains(t, err, "can't parse POST /software")
	assert.Less(t, time.Since(start), testOperationTimeout)
}

func TestPatchSoftware_boundsRetryAfterWait(t *testing.T) {
	var attempts atomic.Int32

	client := newTimeoutTestClient(t, testAttemptTimeout, 200*time.Millisecond,
		func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.Header().Set("Retry-After", "3600")
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		})

	start := time.Now()
	err := client.PatchSoftware("id1", "https://example.org/repo.git", nil, ptr("yml"), true)

	assert.ErrorContains(t, err, "can't update software")
	assert.Less(t, time.Since(start), testOperationTimeout)
	assert.EqualValues(t, 1, attempts.Load())
}

func TestPatchCatalogSoftware_retriesWithinTimeouts(t *testing.T) {
	var attempts atomic.Int32

	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(w http.ResponseWriter, _ *http.Request) {
			if attempts.Add(1) == 1 {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)

				return
			}

			_, _ = w.Write([]byte(`{}`))
		})

	err := client.PatchCatalogSoftware("catalog1", "id1", "https://example.org/repo.git", nil, ptr("yml"), true)

	assert.NoError(t, err)
	assert.EqualValues(t, 2, attempts.Load())
}

func TestGetCatalogs_succeedsWithinTimeouts(t *testing.T) {
	client := newTimeoutTestClient(t, testAttemptTimeout, testOperationTimeout,
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data": [{"id": "cat1", "name": "Catalog"}], "links": {}}`))
		})

	catalogs, err := client.GetCatalogs()

	assert.NoError(t, err)
	assert.Len(t, catalogs, 1)
	assert.Equal(t, "cat1", catalogs[0].ID)
}
