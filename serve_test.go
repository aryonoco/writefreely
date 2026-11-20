/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// getStatus requests rawURL and returns the response status, or -1.
func getStatus(rawURL string) int {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return -1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// startTestServer serves h on a loopback port and returns the server and its
// base URL.
func startTestServer(t *testing.T, h http.Handler) (srv *http.Server, base string) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv = &http.Server{Handler: h, ReadHeaderTimeout: time.Minute}
	go func() { _ = srv.Serve(ln) }()
	return srv, "http://" + ln.Addr().String()
}

func TestNewHTTPServerTimeouts(t *testing.T) {
	t.Parallel()
	s := newHTTPServer("localhost:8080", http.NotFoundHandler())

	if s.Addr != "localhost:8080" || s.Handler == nil {
		t.Errorf("Addr = %q and Handler = %v, want localhost:8080 and the handler given", s.Addr, s.Handler)
	}
	if s.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 10s", s.ReadHeaderTimeout)
	}
	if s.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %s, want 2m0s", s.IdleTimeout)
	}
	// Uploads and responses may take long on a slow connection.
	if s.ReadTimeout != 0 || s.WriteTimeout != 0 {
		t.Errorf("ReadTimeout = %s and WriteTimeout = %s, want neither set", s.ReadTimeout, s.WriteTimeout)
	}
}

func TestDrainFinishesInFlight(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	srv, base := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	status := make(chan int, 1)
	go func() { status <- getStatus(base) }()
	<-started

	if err := drain(srv, shutdownDrain); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := <-status; got != http.StatusOK {
		t.Errorf("in-flight request got status %d, want 200", got)
	}
}

func TestDrainBounded(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	srv, base := startTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	defer close(release)

	go getStatus(base)
	<-started

	begin := time.Now()
	err := drain(srv, shutdownDrain)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error = %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(begin); took > shutdownDrain+time.Second {
		t.Errorf("drain took %s, want at most %s", took, shutdownDrain+time.Second)
	}
}
