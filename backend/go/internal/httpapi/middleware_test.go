package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareDirect(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	run := func(h http.Handler, auth string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	t.Run("empty configured token disables auth", func(t *testing.T) {
		if got := run(authMiddleware("")(ok), ""); got != http.StatusOK {
			t.Fatalf("status = %d, want 200 with empty token config", got)
		}
	})

	h := authMiddleware("secret")(ok)
	cases := []struct {
		name string
		auth string
		want int
	}{
		{name: "correct token", auth: "Bearer secret", want: http.StatusOK},
		{name: "incorrect token", auth: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "missing header", auth: "", want: http.StatusUnauthorized},
		{name: "partial token", auth: "Bearer secre", want: http.StatusUnauthorized},
		{name: "non-bearer scheme", auth: "Basic c2VjcmV0", want: http.StatusUnauthorized},
		{name: "case-sensitive scheme", auth: "bearer secret", want: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(h, tc.auth); got != tc.want {
				t.Fatalf("status = %d, want %d (auth=%q)", got, tc.want, tc.auth)
			}
		})
	}
}