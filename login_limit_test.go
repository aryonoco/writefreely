//go:build sqlite

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
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/writeas/web-core/auth"
	"github.com/writeas/web-core/log"
	"golang.org/x/crypto/bcrypt"
)

func TestLimiterBlocksAddressAfterFailures(t *testing.T) {
	t.Parallel()
	l := newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
	now := time.Now()

	for i := range 10 {
		if !l.allow("198.51.100.1", now) {
			t.Fatalf("attempt %d refused after %d failures", i+1, i)
		}
	}
	if l.allow("198.51.100.1", now) {
		t.Error("address allowed after ten failures")
	}
	if !l.allow("198.51.100.2", now) {
		t.Error("another address refused")
	}
}

func TestLimiterConcurrentAttemptsCapped(t *testing.T) {
	t.Parallel()
	now := time.Now()

	// Whether attempts overlap depends on scheduling, so repeat the burst
	for round := range 100 {
		l := newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
		begin := make(chan struct{})
		var allowed atomic.Int32
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				<-begin
				if l.allow("198.51.100.1", now) {
					allowed.Add(1)
				}
			})
		}
		close(begin)
		wg.Wait()
		if n := allowed.Load(); n != 10 {
			t.Fatalf("round %d: %d of 50 simultaneous attempts allowed, want 10", round+1, n)
		}
	}

	// The tenth attempt succeeds, so it no longer counts
	l := newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
	for i := range 10 {
		if !l.allow("198.51.100.2", now) {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	l.succeeded("198.51.100.2")
	if !l.allow("198.51.100.2", now) {
		t.Error("attempt after nine failures and a success refused")
	}
	if l.allow("198.51.100.2", now) {
		t.Error("address allowed after ten failures")
	}
}

func TestLimiterBounded(t *testing.T) {
	t.Parallel()
	l := newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
	start := time.Now()

	// The first address uses up its attempts before any other is seen, so
	// it is the oldest entry once the table is full.
	for range loginFailureLimit {
		l.allow("198.51.100.1", start)
	}
	addr := netip.MustParseAddr("10.0.0.0")
	for i := range 10000 {
		addr = addr.Next()
		l.allow(addr.String(), start.Add(time.Duration(i+1)*time.Millisecond))
	}

	if n := len(l.entries); n > 10000 {
		t.Errorf("table holds %d addresses, want at most 10000", n)
	}
	if !l.allow("198.51.100.1", start.Add(time.Minute)) {
		t.Error("oldest address still tracked after the table filled")
	}
	if _, ok := l.entries[addr.String()]; !ok {
		t.Error("newest address not tracked")
	}
}

func TestLimiterExpires(t *testing.T) {
	t.Parallel()
	l := newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
	start := time.Now()

	for range 10 {
		l.allow("198.51.100.1", start)
	}
	if l.allow("198.51.100.1", start.Add(15*time.Minute-time.Second)) {
		t.Error("address allowed before the window passed")
	}
	if !l.allow("198.51.100.1", start.Add(15*time.Minute)) {
		t.Error("address still refused after the window passed")
	}
	if a := l.entries["198.51.100.1"]; a == nil || a.count != 1 {
		t.Errorf("entry after the window = %+v, want a new window with one attempt", a)
	}
}

const testSocketPath = "/run/writefreely.sock"

func newForwardedRequest(t *testing.T, remoteAddr, forwardedFor string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", http.NoBody)
	r.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		r.Header.Set("X-Forwarded-For", forwardedFor)
	}
	return r
}

// onUnixSocket returns r as the server sees it when it arrives on a Unix
// socket listener.
func onUnixSocket(r *http.Request) *http.Request {
	sock := &net.UnixAddr{Name: testSocketPath, Net: "unix"}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, sock))
	r.RemoteAddr = "@"
	return r
}

func TestClientAddrUsesTrustedForwardedFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, trusted, remote, forwarded, want string
		socket                                 bool
	}{
		{"trusted peer", "10.0.0.2", "10.0.0.2:41234", "203.0.113.5, 10.0.0.2", "203.0.113.5", false},
		{"entries left of the client", "10.0.0.2", "10.0.0.2:41234", "192.0.2.66, 203.0.113.5", "203.0.113.5", false},
		{"trusted prefix", "10.0.0.0/8", "10.0.0.2:41234", "203.0.113.5, 10.9.8.7", "203.0.113.5", false},
		{"no header", "10.0.0.2", "10.0.0.2:41234", "", "10.0.0.2", false},
		{"IPv6", "2001:db8::/32", "[2001:db8::2]:443", "198.51.100.7, 2001:db8:ffff::1", "198.51.100.7", false},
		{"unix listed", "unix", "", "203.0.113.5", "203.0.113.5", true},
		{"unix and a TCP proxy listed", "unix, 10.0.0.2", "", "203.0.113.5, 10.0.0.2", "203.0.113.5", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			trusted, err := parseTrustedProxies(tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			r := newForwardedRequest(t, tc.remote, tc.forwarded)
			if tc.socket {
				r = onUnixSocket(r)
			}
			if got := clientAddr(r, trusted); got != tc.want {
				t.Errorf("clientAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientAddrIgnoresUntrustedForwardedFor(t *testing.T) {
	t.Parallel()
	for _, list := range []string{"", "10.0.0.2", "10.0.0.0/8, 192.168.0.0/16", "unix"} {
		trusted, err := parseTrustedProxies(list)
		if err != nil {
			t.Fatal(err)
		}
		r := newForwardedRequest(t, "198.51.100.9:5555", "203.0.113.5")
		if got := clientAddr(r, trusted); got != "198.51.100.9" {
			t.Errorf("trusted_proxies %q: clientAddr = %q, want 198.51.100.9", list, got)
		}
	}
}

func TestClientAddrIgnoresUnlistedSocketForwardedFor(t *testing.T) {
	t.Parallel()
	for _, list := range []string{"", "10.0.0.2", "0.0.0.0/0, ::/0"} {
		trusted, err := parseTrustedProxies(list)
		if err != nil {
			t.Fatal(err)
		}
		r := onUnixSocket(newForwardedRequest(t, "", "203.0.113.5"))
		if got := clientAddr(r, trusted); got != "@" {
			t.Errorf("trusted_proxies %q: clientAddr = %q, want the peer @", list, got)
		}
	}
}

// TestClientAddrUnixSocketListener checks clientAddr against a real Unix
// socket listener, where net/http sets the connection's addresses.
func TestClientAddrUnixSocketListener(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "wf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			t.Error(rmErr)
		}
	})
	path := filepath.Join(dir, "s")

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	unixListed, err := parseTrustedProxies("unix")
	if err != nil {
		t.Fatal(err)
	}
	// The peer's address, then clientAddr with an empty list and with unix
	seen := make(chan [3]string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen <- [3]string{r.RemoteAddr, clientAddr(r, proxyList{}), clientAddr(r, unixListed)}
	}))
	if closeErr := srv.Listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://writefreely/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Error(closeErr)
	}

	got := <-seen
	if got[1] != got[0] {
		t.Errorf("unlisted socket: clientAddr = %q, want the peer %q", got[1], got[0])
	}
	if got[2] != "203.0.113.5" {
		t.Errorf("unix listed: clientAddr = %q, want 203.0.113.5", got[2])
	}
}

// syncBuffer is a bytes.Buffer that two loggers can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

//nolint:paralleltest // swaps web-core's package-level loggers and resets the package-level warn-once flag
func TestClientAddrWarnsOnce(t *testing.T) {
	cases := []struct {
		name     string
		request  func() *http.Request
		wantPeer string
	}{
		{"untrusted TCP peer", func() *http.Request {
			return newForwardedRequest(t, "198.51.100.9:5555", "203.0.113.5")
		}, "198.51.100.9"},
		{"unlisted socket peer", func() *http.Request {
			return onUnixSocket(newForwardedRequest(t, "", "203.0.113.5"))
		}, testSocketPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out syncBuffer
			infoOut, errorOut := log.InfoLog.Writer(), log.ErrorLog.Writer()
			log.InfoLog.SetOutput(&out)
			log.ErrorLog.SetOutput(&out)
			t.Cleanup(func() {
				log.InfoLog.SetOutput(infoOut)
				log.ErrorLog.SetOutput(errorOut)
			})
			forwardedForWarned.Store(false)

			for range 2 {
				clientAddr(tc.request(), proxyList{})
			}

			var warnings []string
			for line := range strings.SplitSeq(out.String(), "\n") {
				if strings.Contains(line, "trusted_proxies") {
					warnings = append(warnings, line)
				}
			}
			if len(warnings) != 1 {
				t.Fatalf("got %d warning lines, want 1:\n%s", len(warnings), out.String())
			}
			if !strings.Contains(warnings[0], "[server] trusted_proxies") || !strings.Contains(warnings[0], tc.wantPeer) {
				t.Errorf("warning doesn't name [server] trusted_proxies and the peer %s: %s", tc.wantPeer, warnings[0])
			}
			if strings.Contains(warnings[0], "203.0.113.5") {
				t.Errorf("warning repeats the header's contents: %s", warnings[0])
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()
	got, err := parseTrustedProxies(" 10.0.0.2, 192.168.0.0/16,unix, 2001:db8::/32 ,")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.2/32"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	if !slices.Equal(got.prefixes, want) {
		t.Errorf("prefixes = %v, want %v", got.prefixes, want)
	}
	if !got.unix {
		t.Error("unix entry not recorded")
	}
	if pl, err := parseTrustedProxies("10.0.0.2"); err != nil || pl.unix {
		t.Errorf("list without unix: %+v, %v", pl, err)
	}

	for _, bad := range []string{"localhost", "10.0.0.300", "10.0.0.0/33", "10.0.0.2:80", "unix:/run/writefreely.sock"} {
		if _, err := parseTrustedProxies(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

//nolint:paralleltest // Initialize sets the package-level debugging flag
func TestInitializeRefusesBadTrustedProxies(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(cfgFile, []byte("[server]\ntrusted_proxies = 10.0.0.2, proxy.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Initialize(NewApp(cfgFile), false)
	if err == nil || !strings.Contains(err.Error(), "[server] trusted_proxies") {
		t.Fatalf("Initialize error = %v, want one naming [server] trusted_proxies", err)
	}
}

func TestUnknownUserHashCost(t *testing.T) {
	t.Parallel()
	hashed, err := auth.HashPass([]byte("a password"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := bcrypt.Cost(hashed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bcrypt.Cost([]byte(unknownUserHash))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("unknown-user hash has cost %d, passwords are hashed at %d", got, want)
	}
}

// createLoginTestUser adds a user with a password, hashed at bcrypt's
// lowest cost to keep the tests quick.
func createLoginTestUser(t *testing.T, app *App, username, pass string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{
		Username:   username,
		HashedPass: hash,
		HasPass:    true,
		Created:    time.Now().Truncate(time.Second).UTC(),
	}
	if err := app.db.CreateUser(app.cfg, u, username, ""); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

// apiLogin posts a JSON login to the API from remoteAddr.
func apiLogin(t *testing.T, h http.Handler, remoteAddr, alias, pass string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"alias": alias, "pass": pass})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// webLoginFlashes posts the login form from remoteAddr and returns the
// response with the flash messages it left in the session.
func webLoginFlashes(t *testing.T, app *App, h http.Handler, remoteAddr, alias, pass string) (rec *httptest.ResponseRecorder, flashes []any) {
	t.Helper()
	token, csrfCookie := fetchCSRFToken(t, h, "/login")
	form := url.Values{"alias": {alias}, "pass": {pass}, "gorilla.csrf.Token": {token}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	req.RemoteAddr = remoteAddr
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	next := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", http.NoBody)
	for _, c := range rec.Result().Cookies() {
		next.AddCookie(c)
	}
	session, err := app.sessionStore.Get(next, cookieName)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	return rec, session.Flashes()
}

func TestLoginSameAnswerUnknownAndWrong(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)
	createLoginTestUser(t, app, "owner", "the-right-password")
	router := InitRoutes(app, mux.NewRouter())

	t.Run("api", func(t *testing.T) {
		t.Parallel()
		unknown := apiLogin(t, router, "198.51.100.1:1000", "nobody", "a-password")
		wrong := apiLogin(t, router, "198.51.100.2:1000", "owner", "a-password")

		if unknown.Code != wrong.Code || unknown.Body.String() != wrong.Body.String() {
			t.Errorf("unknown name: %d %s\nwrong password: %d %s",
				unknown.Code, unknown.Body, wrong.Code, wrong.Body)
		}
		if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), "Incorrect username or password.") {
			t.Errorf("wrong password: %d %s, want 401 Incorrect username or password.", wrong.Code, wrong.Body)
		}
	})

	t.Run("web", func(t *testing.T) {
		t.Parallel()
		unknown, unknownFlashes := webLoginFlashes(t, app, router, "198.51.100.3:1000", "nobody", "a-password")
		wrong, wrongFlashes := webLoginFlashes(t, app, router, "198.51.100.4:1000", "owner", "a-password")

		if unknown.Code != wrong.Code || unknown.Header().Get("Location") != wrong.Header().Get("Location") ||
			unknown.Body.String() != wrong.Body.String() {
			t.Errorf("unknown name: %d %q %s\nwrong password: %d %q %s",
				unknown.Code, unknown.Header().Get("Location"), unknown.Body,
				wrong.Code, wrong.Header().Get("Location"), wrong.Body)
		}
		if len(wrongFlashes) != 1 || wrongFlashes[0] != "Incorrect username or password." {
			t.Errorf("wrong password flashes %v, want [Incorrect username or password.]", wrongFlashes)
		}
		if len(unknownFlashes) != 1 || unknownFlashes[0] != wrongFlashes[0] {
			t.Errorf("unknown name flashes %v, wrong password flashes %v", unknownFlashes, wrongFlashes)
		}
	})
}

func TestOwnerNotLockedOutByOthers(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)
	createLoginTestUser(t, app, "owner", "the-right-password")
	router := InitRoutes(app, mux.NewRouter())

	for i := range 50 {
		rec := apiLogin(t, router, "198.51.100.1:1000", "owner", "a-wrong-password")
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("failure %d from A: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := apiLogin(t, router, "198.51.100.1:1000", "owner", "the-right-password"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("A after 50 failures: %d %s, want 429", rec.Code, rec.Body)
	}

	rec := apiLogin(t, router, "203.0.113.20:1000", "owner", "the-right-password")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner from B: %d %s, want 200", rec.Code, rec.Body)
	}
}
