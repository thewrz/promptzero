// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpfed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// A configured MCP server that answers with a redirect must not be followed:
// the configured headers (API keys) would be replayed to whatever host the
// Location header names (GO-2026-6911).
func TestFederationClientsDoNotFollowRedirects(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			var elsewhereHits atomic.Int64
			elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				elsewhereHits.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer elsewhere.Close()
			var configuredHits atomic.Int64
			configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				configuredHits.Add(1)
				http.Redirect(w, r, elsewhere.URL+"/mcp", http.StatusTemporaryRedirect)
			}))
			defer configured.Close()

			client, err := buildClient(ClientConfig{Transport: transport, URL: configured.URL + "/mcp", Headers: map[string]string{"X-Api-Key": "secret"}})
			if err != nil {
				t.Fatalf("buildClient: %v", err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := client.Start(ctx); err == nil {
				_, _ = client.Initialize(ctx, mcp.InitializeRequest{})
			}

			if configuredHits.Load() == 0 {
				t.Fatal("the configured server saw no request; the test did not exercise the transport")
			}
			if hits := elsewhereHits.Load(); hits != 0 {
				t.Fatalf("the client followed the redirect: %d request(s) reached the other server", hits)
			}
		})
	}
}
