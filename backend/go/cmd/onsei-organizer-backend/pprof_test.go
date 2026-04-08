package main

import (
	"net/http"
	"testing"
	"time"
)

func TestStartPprofServer_UsesConfiguredAddress(t *testing.T) {
	called := make(chan struct {
		addr    string
		handler http.Handler
	}, 1)

	startPprofServer("127.0.0.1:6060", func(addr string, handler http.Handler) error {
		called <- struct {
			addr    string
			handler http.Handler
		}{addr: addr, handler: handler}
		return nil
	})

	select {
	case got := <-called:
		if got.addr != "127.0.0.1:6060" {
			t.Fatalf("expected pprof addr 127.0.0.1:6060, got %q", got.addr)
		}
		if got.handler != nil {
			t.Fatalf("expected nil handler (DefaultServeMux), got %#v", got.handler)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected startPprofServer to invoke HTTP serve function")
	}
}
