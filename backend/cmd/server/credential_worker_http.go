package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// configureCredentialWorkerHTTP restricts the HA worker's callback server to
// its own network namespace. The selected handlers still require the existing
// per-process worker token and enforce task ownership.
func configureCredentialWorkerHTTP(server *http.Server) error {
	if server == nil || server.Handler == nil {
		return errors.New("credential worker HTTP handler is unavailable")
	}
	host, port, err := net.SplitHostPort(server.Addr)
	if err != nil {
		return errors.New("credential worker HTTP address is invalid")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("credential worker HTTP port is invalid")
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	default:
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return errors.New("credential worker HTTP requires a loopback or wildcard server host")
		}
	}
	server.Addr = net.JoinHostPort(host, port)
	server.Handler = credentialWorkerHTTPHandler(server.Handler)
	return nil
}

func credentialWorkerHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !credentialWorkerHTTPAllowed(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Keep the boundary exact: a prefix match would accidentally expose future
// admin routes, redirects, or encoded paths through this private listener.
func credentialWorkerHTTPAllowed(r *http.Request) bool {
	const prefix = "/api/v1/internal/openai-reauth/"
	if r.Method != http.MethodPost || r.URL == nil || r.URL.EscapedPath() != r.URL.Path || !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	switch path {
	case "claim", "session-export/claim", "totp-claim", "runtime-settings":
		return true
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[0] {
		return false
	}
	switch parts[1] {
	case "session-export/progress", "session-export/result", "session-export/fail",
		"totp-phase", "totp-finish", "totp-recover", "progress", "callback", "credentials", "fail":
		return true
	}
	return false
}
