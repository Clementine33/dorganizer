package httpapi

import (
	"log"
	"net/http"
	"strings"
)

// authMiddleware enforces a static bearer token on protected routes. An empty
// configured token disables auth entirely (local-developer mode).
func authMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			const prefix = "Bearer "
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, prefix) || strings.TrimPrefix(header, prefix) != token {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// corsMiddleware echoes the configured allowlisted origins. Origins outside
// the allowlist get no CORS headers (browsers then block the response).
func corsMiddleware(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; !ok {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseState struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *responseState) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status < http.StatusOK && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseState) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

func (w *responseState) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &responseState{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("http panic: %v", recovered)
				if !state.wroteHeader {
					writeJSON(state, http.StatusInternalServerError, errorResponse{Code: "INTERNAL", Message: "internal server error"})
				}
			}
		}()
		next.ServeHTTP(state, r)
	})
}
