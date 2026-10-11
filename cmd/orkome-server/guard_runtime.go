package main

import (
	"net/http"
	"strings"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
)

func runtimeAgentWriteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p == nil || p.AgentID == "" ||
			r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if runtimeAgentWriteAllowed(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "runtime credential write scope exceeded", nil)
	})
}

func runtimeAgentWriteAllowed(method, path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(seg) == 4 && seg[1] == "tasks" && method == http.MethodPost:
		switch seg[3] {
		case "claim", "messages", "complete", "fail":
			return true
		}
	case len(seg) == 4 && seg[1] == "work-items" && seg[3] == "comments" && method == http.MethodPost:
		return true
	case len(seg) == 5 && seg[1] == "mcp" && seg[2] == "w" && seg[4] == "agent":
		return true
	}
	return false
}
