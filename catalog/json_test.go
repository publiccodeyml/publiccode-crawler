package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONDriverEnumerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"repositories":[{"url":"https://github.com/example/first"},{"url":"https://gitlab.example/second"},{"url":"%"},{"url":42}]}`)
	}))
	defer server.Close()

	catalogURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	urls, err := NewJSONDriver("$.repositories[*].url").Enumerate(context.Background(), *catalogURL)

	require.NoError(t, err)
	assert.Equal(t, []url.URL{
		{Scheme: "https", Host: "github.com", Path: "/example/first"},
		{Scheme: "https", Host: "gitlab.example", Path: "/second"},
	}, urls)
}

func TestJSONDriverEnumerateErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		jsonPath string
		contains string
	}{
		{
			name:     "HTTP status",
			status:   http.StatusBadGateway,
			jsonPath: "$.repositories[*].url",
			contains: "status 502",
		},
		{
			name:     "invalid JSON",
			status:   http.StatusOK,
			body:     "{",
			jsonPath: "$.repositories[*].url",
			contains: "decode",
		},
		{
			name:     "invalid JSONPath",
			status:   http.StatusOK,
			body:     `{"repositories":[]}`,
			jsonPath: "$.repositories[",
			contains: "jsonpath",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()

			catalogURL, err := url.Parse(server.URL)
			require.NoError(t, err)

			urls, err := NewJSONDriver(test.jsonPath).Enumerate(context.Background(), *catalogURL)

			require.Error(t, err)
			assert.ErrorContains(t, err, test.contains)
			assert.Nil(t, urls)
		})
	}
}
