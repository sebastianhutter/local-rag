package mcp

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/server"
)

// EndpointPath is where the Streamable HTTP transport answers.
const EndpointPath = "/mcp"

// NewHTTPServer returns an http.Server that serves the MCP Streamable HTTP
// transport on 127.0.0.1:port at EndpointPath. The caller starts it with
// ListenAndServe and stops it with Shutdown.
//
// Streamable HTTP replaced the separate SSE transport in the MCP spec of
// 2025-03-26: a client POSTs JSON-RPC to a single endpoint, which an SSE-only
// server answers with 405.
func NewHTTPServer(mcpServer *server.MCPServer, port int) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(EndpointPath, localOriginOnly(server.NewStreamableHTTPServer(mcpServer)))
	return &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}
}

// localOriginOnly rejects browser requests from any origin other than
// localhost. Binding to 127.0.0.1 does not stop a web page in the user's own
// browser from reaching the server -- through DNS rebinding it can even read
// the response -- and this server answers with the user's mail and notes. The
// MCP spec requires servers to validate Origin for exactly this reason.
// Requests without an Origin header come from non-browser clients and pass.
func localOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !isLocalOrigin(origin) {
			slog.Warn("MCP request rejected: non-local origin", "origin", origin)
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalOrigin reports whether an Origin header names a loopback host.
func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
