package httpx

import (
	"net/http"
	"strings"
)

var defaultAllowedOrigins = []string{
	"http://localhost:5173",
	"http://127.0.0.1:5173",
}

func WithCORS(next http.Handler) http.Handler {
	return WithCORSOrigins(next, defaultAllowedOrigins)
}

func WithCORSOrigins(next http.Handler, allowedOrigins []string) http.Handler {
	allowedSet, allowAll := buildOriginAllowlist(allowedOrigins)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		switch {
		case allowAll:
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Vary", "Origin")
		case origin != "" && allowedSet[origin]:
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func buildOriginAllowlist(origins []string) (map[string]bool, bool) {
	allowAll := false
	allowlist := make(map[string]bool, len(origins))
	for _, origin := range origins {
		clean := strings.TrimSpace(origin)
		if clean == "" {
			continue
		}
		if clean == "*" {
			allowAll = true
			continue
		}
		allowlist[clean] = true
	}
	return allowlist, allowAll
}
