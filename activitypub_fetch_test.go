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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

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
