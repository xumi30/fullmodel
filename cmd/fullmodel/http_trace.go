package main

import (
	"net/http"
	"strings"

	agentruntime "github.com/xumi30/fullmodel/agent/runtime"
)

// traceForRequest returns a correlation id: prefers X-Client-Trace-Id, then X-Request-Id, else generates one.
// Sets response header X-Fullmodel-Trace-Id before any WriteHeader when the handler calls this first.
func traceForRequest(w http.ResponseWriter, r *http.Request) string {
	s := strings.TrimSpace(r.Header.Get("X-Client-Trace-Id"))
	if s == "" {
		s = strings.TrimSpace(r.Header.Get("X-Request-Id"))
	}
	if s == "" {
		s, _ = agentruntime.RandomPublicID("trace")
	}
	w.Header().Set("X-Fullmodel-Trace-Id", s)
	return s
}
