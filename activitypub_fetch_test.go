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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/writefreely/writefreely/config"
)

// newFetchTestApp builds an App backed by a temporary SQLite database, with
// the instance actor whose key signs outgoing ActivityPub requests.
func newFetchTestApp(t *testing.T) *App {
	t.Helper()
	if !SQLiteEnabled {
		t.Skip("SQLite support not compiled in; run with `go test -tags sqlite` to run this test")
	}

	cfg := config.New()
	cfg.App.Host = "https://writefreely.test"
	cfg.App.Federation = true
	cfg.Database.Type = driverSQLite
	cfg.Database.FileName = filepath.Join(t.TempDir(), "writefreely.db")

	app := &App{cfg: cfg}
	connectToDatabase(app)
	t.Cleanup(func() {
		if err := app.db.Close(); err != nil {
			t.Errorf("close test db: %v", err)
		}
	})
	if err := adminInitDatabase(app); err != nil {
		t.Fatalf("init database: %v", err)
	}

	prev := instanceColl
	initActivityPub(app)
	t.Cleanup(func() { instanceColl = prev })
	return app
}

// allowTestServerDial lets the address-checked client reach srv, a test
// server on a loopback address, until the test ends. Other addresses stay
// refused unless an earlier call allowed them.
func allowTestServerDial(t *testing.T, srv *httptest.Server) {
	t.Helper()
	addr := srv.Listener.Addr().String()
	prev := dialAllowedForTest
	dialAllowedForTest = func(a string) bool {
		return a == addr || (prev != nil && prev(a))
	}
	t.Cleanup(func() { dialAllowedForTest = prev })
}

// newActorServer serves a Person document on every path and counts the
// requests it receives.
func newActorServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		base := "http://" + r.Host
		body, err := json.Marshal(map[string]any{
			"@context":  []string{"https://www.w3.org/ns/activitystreams"},
			"id":        base + "/actor",
			"type":      "Person",
			"inbox":     base + "/inbox",
			"endpoints": map[string]string{"sharedInbox": base + "/shared-inbox"},
			"url":       base + "/@actor",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/activity+json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func TestFetchRemoteActorRefusesLoopback(t *testing.T) { //nolint:paralleltest // newFetchTestApp sets the package-level instanceColl
	app := newFetchTestApp(t)
	srv, hits := newActorServer(t)

	iri := srv.URL + "/actor"
	if actor, err := fetchRemoteActor(app, iri); err == nil {
		t.Errorf("fetchRemoteActor(%s) = %+v, want an error", iri, actor)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("actor server got %d requests, want 0", n)
	}
}

func TestFetchRemoteActorReadsPerson(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newFetchTestApp(t)
	srv, hits := newActorServer(t)
	allowTestServerDial(t, srv)

	actor, err := fetchRemoteActor(app, srv.URL+"/actor")
	if err != nil {
		t.Fatalf("fetchRemoteActor: %v", err)
	}
	if want := srv.URL + "/inbox"; actor.Inbox != want {
		t.Errorf("Inbox = %q, want %q", actor.Inbox, want)
	}
	if want := srv.URL + "/shared-inbox"; actor.Endpoints.SharedInbox != want {
		t.Errorf("Endpoints.SharedInbox = %q, want %q", actor.Endpoints.SharedInbox, want)
	}
	if want := srv.URL + "/@actor"; actor.URL != want {
		t.Errorf("URL = %q, want %q", actor.URL, want)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("actor server got %d requests, want 1", n)
	}
}

func TestFetchRemoteActorRefusesRedirectToLoopback(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newFetchTestApp(t)
	actorSrv, actorHits := newActorServer(t)
	target := actorSrv.URL + "/actor"

	frontHits := &atomic.Int32{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frontHits.Add(1)
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(front.Close)
	allowTestServerDial(t, front)

	iri := front.URL + "/actor"
	actor, err := fetchRemoteActor(app, iri)
	if !errors.Is(err, errBlockedRemoteAddr) {
		t.Errorf("fetchRemoteActor(%s) = %+v, %v; want an error wrapping %q", iri, actor, err, errBlockedRemoteAddr)
	}
	if n := frontHits.Load(); n != 1 {
		t.Errorf("front server got %d requests, want 1", n)
	}
	if n := actorHits.Load(); n != 0 {
		t.Errorf("redirect target %s got %d requests, want 0", target, n)
	}
}

// serveWebFinger starts a TLS WebFinger server that answers for any
// account with a self link to actorIRI, and points the WebFinger lookup
// at it until the test ends. It returns a handle on that server and the
// number of requests it got.
func serveWebFinger(t *testing.T, actorIRI string) (string, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, err := json.Marshal(map[string]any{
			"subject": r.URL.Query().Get("resource"),
			"links": []map[string]string{
				{"rel": "self", "type": "application/activity+json", "href": actorIRI},
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/jrd+json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	// The WebFinger answer comes from a local TLS test server, so the lookup
	// uses that server's client. The actor fetch that follows is under test.
	prevClient := safeWebfingerHTTPClient
	safeWebfingerHTTPClient = srv.Client()
	t.Cleanup(func() { safeWebfingerHTTPClient = prevClient })

	return "@alice@" + srv.Listener.Addr().String(), hits
}

func TestProfileLookupNeverFetchesPrivate(t *testing.T) { //nolint:paralleltest // serveWebFinger swaps the package-level safeWebfingerHTTPClient
	app := newFetchTestApp(t)
	actorSrv, actorHits := newActorServer(t)
	actorIRI := actorSrv.URL + "/x"
	handle, wfHits := serveWebFinger(t, actorIRI)

	if _, err := GetProfileURLFromHandle(app, handle); err != nil {
		t.Logf("GetProfileURLFromHandle(%s): %v", handle, err)
	}
	if n := wfHits.Load(); n != 1 {
		t.Fatalf("WebFinger server got %d requests, want 1", n)
	}
	if n := actorHits.Load(); n != 0 {
		t.Errorf("actor server at %s got %d requests, want 0", actorIRI, n)
	}
}

func TestProfilePageLookupNeverFetchesPrivate(t *testing.T) { //nolint:paralleltest // serveWebFinger swaps the package-level safeWebfingerHTTPClient
	app := newFetchTestApp(t)
	actorSrv, actorHits := newActorServer(t)
	actorIRI := actorSrv.URL + "/x"
	handle, wfHits := serveWebFinger(t, actorIRI)

	if _, err := app.db.GetProfilePageFromHandle(app, handle); err != nil {
		t.Logf("GetProfilePageFromHandle(%s): %v", handle, err)
	}
	if n := wfHits.Load(); n != 1 {
		t.Fatalf("WebFinger server got %d requests, want 1", n)
	}
	if n := actorHits.Load(); n != 0 {
		t.Errorf("actor server at %s got %d requests, want 0", actorIRI, n)
	}
}

// largeBodyTestSize is the size of the largest bodies these tests serve:
// far over every limit, and more than the socket buffers between a test
// server and its client hold, so a server sends all of it only when its
// client reads all of it.
const largeBodyTestSize = 32 << 20

// largeBodyServer answers every request with a body of size bytes: the
// head its body function returns, then 'a's, then the tail. It counts the
// bytes it sends.
type largeBodyServer struct {
	srv         *httptest.Server
	size        int
	contentType string
	body        func(r *http.Request) (head, tail string)
	sent        atomic.Int64
}

// newLargeBodyServer starts a largeBodyServer with newServer (httptest's
// NewServer or NewTLSServer).
func newLargeBodyServer(t *testing.T, newServer func(http.Handler) *httptest.Server, size int, contentType string, body func(r *http.Request) (head, tail string)) *largeBodyServer {
	t.Helper()
	s := &largeBodyServer{size: size, contentType: contentType, body: body}
	s.srv = newServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *largeBodyServer) serve(w http.ResponseWriter, r *http.Request) {
	// A client that stops reading closes the connection, which ends the
	// writes below; the deadline ends them should it not
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	head, tail := s.body(r)
	w.Header().Set("Content-Type", s.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(s.size))
	write := func(b []byte) bool {
		n, err := w.Write(b)
		s.sent.Add(int64(n))
		return err == nil
	}
	if !write([]byte(head)) {
		return
	}
	chunk := bytes.Repeat([]byte("a"), 32<<10)
	for pad := s.size - len(head) - len(tail); pad > 0; {
		c := chunk[:min(pad, len(chunk))]
		if !write(c) {
			return
		}
		pad -= len(c)
	}
	write([]byte(tail))
}

// finish stops the server, once the requests it is answering are done,
// and returns how many bytes it sent.
func (s *largeBodyServer) finish() int64 {
	s.srv.Close()
	return s.sent.Load()
}

// actorDocumentParts returns the head and tail of a Person document for an
// actor on r's host, whose summary is left open between them.
func actorDocumentParts(r *http.Request) (head, tail string) {
	base := "http://" + r.Host
	return `{"@context":"https://www.w3.org/ns/activitystreams","id":"` + base + `/actor","type":"Person","inbox":"` + base + `/inbox","summary":"`, `"}`
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestReadRemoteDocumentStopsAfterLimit(t *testing.T) {
	t.Parallel()
	for _, size := range []int{maxRemoteDocumentSize, maxRemoteDocumentSize + 1, largeBodyTestSize} {
		r := &countingReader{r: bytes.NewReader(make([]byte, size))}
		b, err := readRemoteDocument(r)
		if size <= maxRemoteDocumentSize {
			if err != nil || len(b) != size {
				t.Errorf("%d bytes: readRemoteDocument = %d bytes, %v; want %d bytes, nil", size, len(b), err, size)
			}
		} else if !errors.Is(err, errRemoteDocumentTooLarge) {
			t.Errorf("%d bytes: readRemoteDocument = %d bytes, %v; want an error wrapping %q", size, len(b), err, errRemoteDocumentTooLarge)
		}
		if want := min(size, maxRemoteDocumentSize+1); r.n != want {
			t.Errorf("%d bytes: readRemoteDocument read %d bytes, want %d", size, r.n, want)
		}
	}
}

func TestFetchRemoteActorLimitsDocumentSize(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newFetchTestApp(t)
	for _, size := range []int{maxRemoteDocumentSize, maxRemoteDocumentSize + 1, largeBodyTestSize} {
		s := newLargeBodyServer(t, httptest.NewServer, size, "application/activity+json", actorDocumentParts)
		allowTestServerDial(t, s.srv)

		actor, err := fetchRemoteActor(app, s.srv.URL+"/actor")
		sent := s.finish()
		t.Logf("%d-byte document: server sent %d bytes; fetchRemoteActor error: %v", size, sent, err)
		if size <= maxRemoteDocumentSize {
			if err != nil || actor.Inbox != s.srv.URL+"/inbox" {
				t.Errorf("%d-byte document: fetchRemoteActor = %+v, %v; want the actor", size, actor, err)
			}
			continue
		}
		if !errors.Is(err, errRemoteDocumentTooLarge) {
			t.Errorf("%d-byte document: fetchRemoteActor error = %v, want one wrapping %q", size, err, errRemoteDocumentTooLarge)
		}
		if size == largeBodyTestSize && sent >= int64(size) {
			t.Errorf("%d-byte document: the server sent all of it, so it was read to its end", size)
		}
	}
}

func TestResolveIRIReportsStatusOfLargeAnswer(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newFetchTestApp(t)
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxRemoteDocumentSize+1))
	}))
	t.Cleanup(gone.Close)
	allowTestServerDial(t, gone)

	_, _, err := resolveIRI(app.cfg.App.Host, gone.URL+"/actor")
	var status *remoteStatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusGone {
		t.Errorf("resolveIRI error = %v, want a remoteStatusError with status 410", err)
	}
}

func TestRemoteLookupLimitsDocumentSize(t *testing.T) { //nolint:paralleltest // the test swaps the package-level safeWebfingerHTTPClient
	const href = "https://remote.test/users/alice"
	webFingerParts := func(r *http.Request) (head, tail string) {
		return `{"subject":"` + r.URL.Query().Get("resource") + `","links":[{"rel":"self","type":"application/activity+json","href":"` + href + `"}],"padding":"`, `"}`
	}
	prevClient := safeWebfingerHTTPClient
	t.Cleanup(func() { safeWebfingerHTTPClient = prevClient })

	for _, size := range []int{maxRemoteDocumentSize, maxRemoteDocumentSize + 1, largeBodyTestSize} {
		s := newLargeBodyServer(t, httptest.NewTLSServer, size, "application/jrd+json", webFingerParts)
		safeWebfingerHTTPClient = s.srv.Client()

		got := RemoteLookup("@alice@" + s.srv.Listener.Addr().String())
		sent := s.finish()
		t.Logf("%d-byte document: server sent %d bytes; RemoteLookup = %q", size, sent, got)
		want := ""
		if size <= maxRemoteDocumentSize {
			want = href
		}
		if got != want {
			t.Errorf("%d-byte document: RemoteLookup = %q, want %q", size, got, want)
		}
		if size == largeBodyTestSize && sent >= int64(size) {
			t.Errorf("%d-byte document: the server sent all of it, so it was read to its end", size)
		}
	}
}

func TestDeliveryReadsLittleOfTheAnswer(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newFetchTestApp(t)
	inbox := newLargeBodyServer(t, httptest.NewServer, largeBodyTestSize, "application/activity+json", func(*http.Request) (head, tail string) {
		return "", ""
	})
	allowTestServerDial(t, inbox.srv)

	activity := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "Create"}
	err := makeActivityPost(app.cfg.App.Host, instanceColl.PersonObject(), inbox.srv.URL+"/inbox", activity)
	sent := inbox.finish()
	t.Logf("%d-byte answer: inbox sent %d bytes; makeActivityPost error: %v", largeBodyTestSize, sent, err)
	if err != nil {
		t.Errorf("makeActivityPost: %v", err)
	}
	if sent >= largeBodyTestSize {
		t.Errorf("the inbox sent all of its %d-byte answer, so it was read to its end", largeBodyTestSize)
	}
}

func TestKeyRefetchesWaitsOutInterval(t *testing.T) {
	t.Parallel()
	start := time.Now()
	now := start
	k := newKeyRefetches(10, func() time.Time { return now })

	if !k.allow("a") {
		t.Fatal("first refetch of a refused")
	}
	now = start.Add(keyRefetchInterval - time.Second)
	if k.allow("a") {
		t.Error("refetch of a within the interval allowed")
	}
	if !k.allow("b") {
		t.Error("first refetch of b refused")
	}
	now = start.Add(keyRefetchInterval)
	if !k.allow("a") {
		t.Error("refetch of a once the interval passed refused")
	}
	if k.allow("b") {
		t.Error("refetch of b within its interval allowed")
	}
}

func TestKeyRefetchesStaysWithinCapacity(t *testing.T) {
	t.Parallel()
	now := time.Now()
	k := newKeyRefetches(3, func() time.Time { return now })

	for i := range 3 {
		k.allow("k" + strconv.Itoa(i))
		now = now.Add(time.Second)
	}
	// Full: k0, the oldest, makes room for k3
	if !k.allow("k3") {
		t.Fatal("refetch of k3 refused")
	}
	if n := len(k.known); n > 3 {
		t.Errorf("%d key IDs remembered, want at most 3", n)
	}
	if k.allow("k2") || k.allow("k3") {
		t.Error("refetch of k2 or k3 allowed within the interval")
	}
	if !k.allow("k0") {
		t.Error("refetch of k0, dropped to make room, refused")
	}
}

func TestKeyRefetchesConcurrentUse(t *testing.T) {
	t.Parallel()
	k := newKeyRefetches(10, time.Now)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if k.allow("a") {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 1 {
		t.Errorf("%d of 50 refetches at once allowed, want 1", n)
	}
}
