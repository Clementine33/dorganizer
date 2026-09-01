package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSSEWriterSendsAndFlushesEvent(t *testing.T) {
	recorder := httptest.NewRecorder()

	writer, err := newSSEWriter(recorder)
	if err != nil {
		t.Fatalf("newSSEWriter: %v", err)
	}
	if err := writer.Send("progress", struct {
		Files int `json:"files"`
	}{Files: 3}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	const want = "event: progress\ndata: {\"files\":3}\n\n"
	if got := recorder.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if !recorder.Flushed {
		t.Fatal("event was not flushed")
	}
}

func decodeJSONError(r *http.Request, dst any) error {
	w := httptest.NewRecorder()
	return decodeJSON(w, r, dst)
}

func TestDecodeJSON(t *testing.T) {
	t.Run("valid object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Music"}`))
		var got struct {
			Name string `json:"name"`
		}
		if err := decodeJSONError(req, &got); err != nil {
			t.Fatalf("decodeJSON: %v", err)
		}
		if got.Name != "Music" {
			t.Fatalf("name = %q, want Music", got.Name)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if err := decodeJSONError(req, &struct{}{}); err == nil {
			t.Fatal("empty body should fail")
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Music","future":true}`))
		var got struct {
			Name string `json:"name"`
		}
		err := decodeJSONError(req, &got)
		var pe *payloadError
		if !errors.As(err, &pe) {
			t.Fatalf("expected payloadError, got %T: %v", err, err)
		}
		if pe.status != http.StatusBadRequest || pe.code != "INVALID_ARGUMENT" {
			t.Fatalf("expected 400 INVALID_ARGUMENT, got %d %s", pe.status, pe.code)
		}
	})

	t.Run("trailing content rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Music"}{"ignored":true}`))
		if err := decodeJSONError(req, &struct {
			Name string `json:"name"`
		}{}); err == nil {
			t.Fatal("trailing JSON should fail")
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name": `))
		if err := decodeJSONError(req, &struct {
			Name string `json:"name"`
		}{}); err == nil {
			t.Fatal("malformed JSON should fail")
		}
	})

	t.Run("oversized body rejected with 413", func(t *testing.T) {
		body := `{"name":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		err := decodeJSONError(req, &struct {
			Name string `json:"name"`
		}{})
		var pe *payloadError
		if !errors.As(err, &pe) {
			t.Fatalf("expected payloadError, got %T: %v", err, err)
		}
		if pe.status != http.StatusRequestEntityTooLarge || pe.code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected 413 PAYLOAD_TOO_LARGE, got %d %s", pe.status, pe.code)
		}
	})
}

func TestDecodeJSONAllowEmpty(t *testing.T) {
	t.Run("empty body decodes to zero value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		var got struct {
			RootPath *string `json:"root_path"`
		}
		w := httptest.NewRecorder()
		if err := decodeJSONAllowEmpty(w, req, &got); err != nil {
			t.Fatalf("decodeJSONAllowEmpty: %v", err)
		}
		if got.RootPath != nil {
			t.Fatalf("expected nil root_path for empty body, got %q", *got.RootPath)
		}
	})

	t.Run("valid object still decodes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"root_path":"/music"}`))
		var got struct {
			RootPath *string `json:"root_path"`
		}
		w := httptest.NewRecorder()
		if err := decodeJSONAllowEmpty(w, req, &got); err != nil {
			t.Fatalf("decodeJSONAllowEmpty: %v", err)
		}
		if got.RootPath == nil || *got.RootPath != "/music" {
			t.Fatalf("root_path = %v, want /music", got.RootPath)
		}
	})

	t.Run("malformed non-empty body rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json`))
		w := httptest.NewRecorder()
		if err := decodeJSONAllowEmpty(w, req, &struct{}{}); err == nil {
			t.Fatal("malformed body should fail even for allow-empty decode")
		}
	})
}

func TestWriteJSONPreservesExactBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusCreated, struct {
		OK bool `json:"ok"`
	}{OK: true})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want exact JSON without newline", got)
	}
}

func TestRecoveryMiddlewareReturnsInternalEnvelope(t *testing.T) {
	handler := recoveryMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"code":"INTERNAL","message":"internal server error"}` {
		t.Fatalf("body = %q", got)
	}
}

type informationalResponseWriter struct {
	header   http.Header
	statuses []int
	body     bytes.Buffer
}

func (w *informationalResponseWriter) Header() http.Header { return w.header }
func (w *informationalResponseWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}
func (w *informationalResponseWriter) Write(payload []byte) (int, error) {
	return w.body.Write(payload)
}

func TestRecoveryMiddlewareCanRecoverAfterEarlyHints(t *testing.T) {
	handler := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		panic("boom")
	}))
	writer := &informationalResponseWriter{header: make(http.Header)}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))

	if got, want := writer.statuses, []int{
		http.StatusEarlyHints,
		http.StatusInternalServerError,
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if got := writer.body.String(); got != `{"code":"INTERNAL","message":"internal server error"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestRecoveryMiddlewareDoesNotReplaceSwitchingProtocols(t *testing.T) {
	handler := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		panic("boom")
	}))
	writer := &informationalResponseWriter{header: make(http.Header)}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))

	if got, want := writer.statuses, []int{http.StatusSwitchingProtocols}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if writer.body.Len() != 0 {
		t.Fatalf("body = %q, want empty", writer.body.String())
	}
}
