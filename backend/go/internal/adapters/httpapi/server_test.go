package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/library"
)

// newTestServer builds a router with a fresh temp repository and the given
// dependency overrides. As in server.go, no gate is wired unless a test asks
// for one: the library entry and the scan routes run without admission, which
// is the state a process without file management is in.
func newTestServer(t *testing.T, mutate func(*Dependencies)) http.Handler {
	t.Helper()
	repo := newHTTPTestRepository(t)
	deps := Dependencies{
		Repo:        repo,
		Library:     library.NewService(repo, nil),
		Token:       "",
		CORSOrigins: []string{},
		Version:     "dev",
	}
	if mutate != nil {
		mutate(&deps)
	}
	return NewServer(deps)
}

// testGate installs one gate into every path that takes a slot — the library
// service (root change, deletion) and the scan routes — so a test that holds
// the slot really refuses both. Replacing only Dependencies.Gate would leave
// the library service holding its own, and the test would pass vacuously.
func testGate(d *Dependencies, gate *admission.Gate) {
	d.Gate = gate
	d.Library = library.NewService(d.Repo, gate)
}

// newHTTPTestRepository opens a repository on a fresh temp DB file.
func newHTTPTestRepository(t *testing.T) *sqlite.Repository {
	t.Helper()
	tmpFile, err := os.CreateTemp(t.TempDir(), "onsei-httpapi-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	repo, err := sqlite.NewRepository(tmpPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// doRequest performs a request against the engine and returns the recorder.
func doRequest(
	t *testing.T,
	engine http.Handler,
	method, path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// createLibraryViaAPI creates a library through the API and returns its ID.
// It is a shared helper: the library, record and tree tests all start here.
func createLibraryViaAPI(t *testing.T, engine http.Handler, name, rootPath string) string {
	t.Helper()
	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries",
		map[string]string{"name": name, "root_path": rootPath}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var lib libraryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	return lib.ID
}

// errorEnvelope decodes the standard error envelope body.
func errorEnvelope(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, w.Body.String())
	}
	return env.Code, env.Message
}

// ptr returns a pointer to a copy of v for nullable DTO fixture fields.
//
// The body uses new(expr) (Go 1.26+) rather than &v so the modernize newexpr
// analyzer stays quiet. Do not add //go:fix inline: govet's inline pass would
// then report every call site, and it cannot inline generic calls at all
// ("type parameter inference is not yet supported"). Call sites must pass a
// typed value, e.g. ptr(int64(320)), since inference would otherwise pick the
// untyped constant's default type (int).
func ptr[T any](v T) *T { return new(v) }

func TestHealthEndpoint(t *testing.T) {
	engine := newTestServer(t, nil)

	w := doRequest(t, engine, http.MethodGet, "/api/v1/health", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ok":true,"version":"dev"}` {
		t.Fatalf("body = %s, want %s", got, `{"ok":true,"version":"dev"}`)
	}
}

func TestAuthMiddleware(t *testing.T) {
	t.Run("missing header is rejected", func(t *testing.T) {
		engine := newTestServer(t, func(d *Dependencies) { d.Token = "t1" })

		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries", nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "UNAUTHORIZED" {
			t.Fatalf("code = %q, want UNAUTHORIZED", code)
		}
	})

	t.Run("valid bearer token passes", func(t *testing.T) {
		engine := newTestServer(t, func(d *Dependencies) { d.Token = "t1" })

		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries", nil,
			map[string]string{"Authorization": "Bearer t1"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("empty token config bypasses auth", func(t *testing.T) {
		engine := newTestServer(t, nil) // Token == ""

		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("health needs no auth", func(t *testing.T) {
		engine := newTestServer(t, func(d *Dependencies) { d.Token = "t1" })

		w := doRequest(t, engine, http.MethodGet, "/api/v1/health", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

func TestCORSMiddleware(t *testing.T) {
	engine := newTestServer(t, func(d *Dependencies) {
		d.CORSOrigins = []string{"http://localhost:5173"}
	})

	t.Run("allowlisted origin is echoed", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/health", nil,
			map[string]string{"Origin": "http://localhost:5173"})
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:5173")
		}
	})

	t.Run("disallowed origin gets no CORS header", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/health", nil,
			map[string]string{"Origin": "http://evil.example"})
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
		}
	})

	t.Run("no origin header gets no CORS header", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/health", nil, nil)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
		}
	})
}

func TestCORSPreflightRunsBeforeAuth(t *testing.T) {
	engine := newTestServer(t, func(d *Dependencies) {
		d.Token = "secret"
		d.CORSOrigins = []string{"http://localhost:5173"}
	})
	w := doRequest(t, engine, http.MethodOptions, "/api/v1/libraries", nil, map[string]string{
		"Origin": "http://localhost:5173",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	// Workset writes carry If-Match / Idempotency-Key; the preflight must
	// allow them or the browser blocks every mutating request.
	allowHeaders := w.Header().Get("Access-Control-Allow-Headers")
	for _, want := range []string{"If-Match", "Idempotency-Key"} {
		if !strings.Contains(allowHeaders, want) {
			t.Fatalf("Access-Control-Allow-Headers = %q, missing %q", allowHeaders, want)
		}
	}
	if methods := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "PUT") {
		t.Fatalf("Access-Control-Allow-Methods = %q, missing PUT", methods)
	}
}

func TestStandardRouterMethodAndPathSemantics(t *testing.T) {
	engine := newTestServer(t, nil)
	t.Run("wrong method returns 405 and Allow", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodPost, "/api/v1/health", nil, nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405 (body=%s)", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) {
			t.Fatalf("Allow = %q, want GET", got)
		}
	})
	t.Run("unknown path returns 404", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/not-a-route", nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}

func TestRouterRejectsPathsServeMuxWouldCanonicalize(t *testing.T) {
	engine := newTestServer(t, nil)
	for _, requestPath := range []string{"/api/v1//health", "/api/v1/x/../health"} {
		t.Run(requestPath, func(t *testing.T) {
			w := doRequest(t, engine, http.MethodGet, requestPath, nil, nil)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			if location := w.Header().Get("Location"); location != "" {
				t.Fatalf("Location = %q, want no redirect", location)
			}
		})
	}
}

func TestRouterRejectsImplicitHEAD(t *testing.T) {
	engine := newTestServer(t, func(d *Dependencies) { d.Token = "secret" })
	for _, testCase := range []struct {
		path        string
		allowMethod string
	}{
		{path: "/api/v1/health", allowMethod: http.MethodGet},
		{path: "/api/v1/libraries", allowMethod: http.MethodGet},
		{path: "/api/v1/libraries/library/scans", allowMethod: http.MethodPost},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			w := doRequest(t, engine, http.MethodHead, testCase.path, nil, nil)
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405 (body=%s)", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Allow"); !strings.Contains(got, testCase.allowMethod) {
				t.Fatalf("Allow = %q, want %s", got, testCase.allowMethod)
			}
		})
	}

	w := doRequest(t, engine, http.MethodHead, "/api/v1/not-a-route", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown HEAD status = %d, want 404", w.Code)
	}
}

func TestRouterRejectsEncodedSlashInPathValues(t *testing.T) {
	engine := newTestServer(t, func(d *Dependencies) { d.Token = "secret" })
	for _, requestPath := range []string{
		"/api/v1/libraries/library%2Fother",
		"/api/v1/libraries/library/folders/folder%2fother/tree",
	} {
		t.Run(requestPath, func(t *testing.T) {
			w := doRequest(t, engine, http.MethodGet, requestPath, nil, nil)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want router-level 404 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}
