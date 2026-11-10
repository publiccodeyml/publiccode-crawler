package scanner

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/italia/publiccode-crawler/v4/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGiteaScannerSetsRequestTimeout(t *testing.T) {
	scanner := NewGiteaScanner()

	assert.Equal(t, giteaRequestTimeout, scanner.httpClient.Timeout)
}

func TestGiteaScannerCancelsStalledResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	repoURL, err := url.Parse(server.URL + "/owner/repository")
	require.NoError(t, err)

	scanner := NewGiteaScanner()
	scanner.httpClient.Timeout = time.Second

	err = scanner.Scan(*repoURL, common.Publisher{}, make(chan common.Repository))

	require.Error(t, err)
	assert.ErrorContains(t, err, "context deadline exceeded")
}
