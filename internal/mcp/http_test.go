package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLocalOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:6274", true},
		{"http://127.0.0.1", true},
		{"http://[::1]:3000", true},
		{"https://example.com", false},
		{"http://localhost.example.com", false},
		{"http://127.0.0.1.example.com", false},
		{"null", false},
	}
	for _, tt := range tests {
		if got := isLocalOrigin(tt.origin); got != tt.want {
			t.Errorf("isLocalOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

// initializeRequest is the first message a Streamable HTTP client POSTs.
const initializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func postInitialize(t *testing.T, h http.Handler, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, EndpointPath, strings.NewReader(initializeRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPServerAcceptsPost(t *testing.T) {
	h := NewHTTPServer(CreateServer(), 0).Handler

	for _, origin := range []string{"", "http://localhost:6274"} {
		rec := postInitialize(t, h, origin)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %q: status %d, body %s", origin, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"serverInfo"`) {
			t.Errorf("origin %q: no serverInfo in initialize response: %s", origin, rec.Body)
		}
	}
}

func TestHTTPServerRejectsForeignOrigin(t *testing.T) {
	h := NewHTTPServer(CreateServer(), 0).Handler
	if rec := postInitialize(t, h, "https://example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d", rec.Code, http.StatusForbidden)
	}
}
