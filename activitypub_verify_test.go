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
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/writeas/httpsig"
	"github.com/writeas/web-core/log"
)

// inboxTestAlias is the blog whose inbox the tests post to.
const inboxTestAlias = "inboxtest"

// inboxTestBlogIRI is the actor of the blog at inboxTestAlias.
const inboxTestBlogIRI = "https://writefreely.test/api/collections/" + inboxTestAlias

// inboxTestKeys are RSA keys for the remote accounts in these tests,
// generated once since each takes a moment.
var inboxTestKeys = sync.OnceValues(func() ([]*rsa.PrivateKey, error) {
	keys := make([]*rsa.PrivateKey, 3)
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		keys[i] = k
	}
	return keys, nil
})

// inboxTestKey returns the i-th of three RSA keys shared by these tests.
func inboxTestKey(t *testing.T, i int) *rsa.PrivateKey {
	t.Helper()
	keys, err := inboxTestKeys()
	if err != nil {
		t.Fatalf("generate RSA keys: %v", err)
	}
	return keys[i]
}

// newInboxTestApp returns an App with one blog, at inboxTestAlias.
func newInboxTestApp(t *testing.T) *App {
	t.Helper()
	app := newFetchTestApp(t)
	u := &User{Username: inboxTestAlias, HashedPass: []byte("unused")}
	if err := app.db.CreateUser(app.cfg, u, "", ""); err != nil {
		t.Fatalf("create blog %q: %v", inboxTestAlias, err)
	}
	return app
}

// inboxTestActor is how inboxTestActors answers for one account.
type inboxTestActor struct {
	// key is the public key the actor document lists.
	key *rsa.PublicKey
	// status, when set, is the answer to every GET of the actor.
	status int
	// owner, when set, is the key's owner instead of the actor itself.
	owner string
	// id and keyID, when set, are the document's id and its key's id
	// instead of the actor's own.
	id, keyID string
	// honestGets is how many GETs get the actor's own document before id,
	// keyID and redirect apply.
	honestGets int
	// redirect, when set, is where GETs are redirected to.
	redirect string
}

// inboxTestActors serves the actor documents of remote accounts at
// /users/<name>, each listing its key as <actor>#main-key, or as
// <actor>/main-key when fetched there, and an inbox at /users/<name>/inbox
// that accepts every POST. It counts the GETs it answers.
type inboxTestActors struct {
	srv  *httptest.Server
	gets atomic.Int32

	mu     sync.Mutex
	actors map[string]inboxTestActor
	served map[string]int
}

// newInboxTestActors starts an inboxTestActors server that the
// address-checked client may reach until the test ends.
func newInboxTestActors(t *testing.T) *inboxTestActors {
	t.Helper()
	a := &inboxTestActors{actors: map[string]inboxTestActor{}, served: map[string]int{}}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	allowTestServerDial(t, a.srv)
	return a
}

// set makes the server answer for name as actor says.
func (a *inboxTestActors) set(name string, actor *inboxTestActor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actors[name] = *actor
}

// iri returns the actor IRI of the account name.
func (a *inboxTestActors) iri(name string) string {
	return a.srv.URL + "/users/" + name
}

// keyID returns the ID of the account name's key.
func (a *inboxTestActors) keyID(name string) string {
	return a.iri(name) + "#main-key"
}

func (a *inboxTestActors) serve(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, "/users/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	a.gets.Add(1)

	name, keyPath := strings.CutSuffix(path, "/main-key")
	a.mu.Lock()
	actor, ok := a.actors[name]
	a.served[name]++
	honest := a.served[name] <= actor.honestGets
	a.mu.Unlock()
	if !honest && actor.redirect != "" {
		http.Redirect(w, r, actor.redirect, http.StatusFound)
		return
	}
	if actor.status != 0 {
		w.Header().Set("Content-Type", "application/activity+json")
		w.WriteHeader(actor.status)
		_, _ = w.Write([]byte(`{"error":"Gone"}`))
		return
	}
	if !ok || actor.key == nil {
		http.NotFound(w, r)
		return
	}

	iri := a.iri(name)
	id, keyID := iri, iri+"#main-key"
	if keyPath {
		keyID = iri + "/main-key"
	}
	if !honest && actor.id != "" {
		id = actor.id
	}
	if !honest && actor.keyID != "" {
		keyID = actor.keyID
	}
	owner := actor.owner
	if owner == "" {
		owner = iri
	}
	pemKey, err := publicKeyPEM(actor.key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(map[string]any{
		"@context":  []string{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":        id,
		"type":      "Person",
		"inbox":     iri + "/inbox",
		"url":       iri,
		"publicKey": map[string]string{"id": keyID, "owner": owner, "publicKeyPem": pemKey},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/activity+json")
	_, _ = w.Write(body)
}

// publicKeyPEM encodes key as a PEM "PUBLIC KEY" block, as actors list it.
func publicKeyPEM(key *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// inboxActivity returns an activity of type typ by actor on object, as
// JSON.
func inboxActivity(t *testing.T, typ, actor string, object any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       actor + "#" + strings.ToLower(typ) + "-" + time.Now().Format("150405.000000000"),
		"type":     typ,
		"actor":    actor,
		"object":   object,
	})
	if err != nil {
		t.Fatalf("marshal %s: %v", typ, err)
	}
	return body
}

// newInboxRequest returns an unsigned POST of body to the inbox of the blog
// at inboxTestAlias.
func newInboxRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://writefreely.test/api/collections/"+inboxTestAlias+"/inbox", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/activity+json")
	return r
}

// signInboxRequest signs r as remote servers sign their deliveries: it adds
// a Digest of body and, unless r has one, a Date of now, then a Signature
// made with key under keyID over the request target, Host, Date and Digest.
func signInboxRequest(t *testing.T, r *http.Request, body []byte, keyID string, key *rsa.PrivateKey) {
	t.Helper()
	sum := sha256.Sum256(body)
	r.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]))
	if r.Header.Get("Date") == "" {
		r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	signer := httpsig.NewSigner(keyID, key, httpsig.RSASHA256, []string{"(request-target)", "host", "date", "digest"})
	if err := signer.SignSigHeader(r); err != nil {
		t.Fatalf("sign inbox request: %v", err)
	}
}

// serveInbox passes r to the inbox handler, wrapped as the router wraps it,
// and returns the response.
func serveInbox(t *testing.T, app *App, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	router := mux.NewRouter()
	router.HandleFunc("/api/collections/{alias}/inbox", NewHandler(app).All(handleFetchCollectionInbox)).Methods(http.MethodPost)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

// inboxRowCount returns the single number query selects.
func inboxRowCount(t *testing.T, app *App, query string, args ...any) int {
	t.Helper()
	var n int
	if err := app.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// followerCount returns how many remote accounts follow the test blog.
func followerCount(t *testing.T, app *App) int {
	t.Helper()
	return inboxRowCount(t, app, "SELECT COUNT(*) FROM remotefollows")
}

// addInboxTestFollower stores actorIRI as a remote account that follows the
// test blog, with its key under keyID when key is set.
func addInboxTestFollower(t *testing.T, app *App, actorIRI, keyID string, key *rsa.PublicKey) {
	t.Helper()
	ctx := t.Context()
	res, err := app.db.ExecContext(ctx, "INSERT INTO remoteusers (actor_id, inbox, shared_inbox, url) VALUES (?, ?, ?, ?)",
		actorIRI, actorIRI+"/inbox", "", actorIRI)
	if err != nil {
		t.Fatalf("add remote user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("remote user id: %v", err)
	}
	if key != nil {
		var pemKey string
		pemKey, err = publicKeyPEM(key)
		if err != nil {
			t.Fatalf("encode key: %v", err)
		}
		_, err = app.db.ExecContext(ctx, "INSERT INTO remoteuserkeys (id, remote_user_id, public_key) VALUES (?, ?, ?)", keyID, id, pemKey)
		if err != nil {
			t.Fatalf("add remote user key: %v", err)
		}
	}
	c, err := app.db.GetCollection(inboxTestAlias)
	if err != nil {
		t.Fatalf("get blog: %v", err)
	}
	if _, err := app.db.ExecContext(ctx, "INSERT INTO remotefollows (collection_id, remote_user_id, created) VALUES (?, ?, "+app.db.now()+")", c.ID, id); err != nil {
		t.Fatalf("add follow: %v", err)
	}
}

// assertInboxStatus fails the test unless rec has status want.
func assertInboxStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("inbox answered %d %s, want %d", rec.Code, strings.TrimSpace(rec.Body.String()), want)
	}
}

func TestInboxRefusesUnsigned(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	actors.set("a", &inboxTestActor{key: &inboxTestKey(t, 0).PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	rec := serveInbox(t, app, newInboxRequest(t, body))

	assertInboxStatus(t, rec, http.StatusUnauthorized)
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxRefusesBadSignature(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	actors.set("a", &inboxTestActor{key: &inboxTestKey(t, 0).PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), inboxTestKey(t, 1))
	rec := serveInbox(t, app, r)

	assertInboxStatus(t, rec, http.StatusUnauthorized)
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxAcceptsSignatureWithoutAlgorithm(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), key)
	r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), `algorithm="rsa-sha256",`, "", 1))
	if strings.Contains(r.Header.Get("Signature"), "algorithm") {
		t.Fatalf("Signature still names an algorithm: %s", r.Header.Get("Signature"))
	}
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusOK)
}

func TestInboxAcceptsKeyIDOfActor(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})
	actors.set("b", &inboxTestActor{key: &inboxTestKey(t, 1).PublicKey})
	actors.set("c", &inboxTestActor{key: &key.PublicKey, owner: "https://elsewhere.test/users/c"})

	// The actor's own IRI as keyId, for a key it lists as <actor>#main-key
	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.iri("a"), key)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusOK)

	// Another actor's IRI as keyId, for a Follow by A
	keyB := inboxTestKey(t, 1)
	body = inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r = newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.iri("b"), keyB)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)

	// The actor's IRI as keyId, for a key whose owner is on another host
	body = inboxActivity(t, "Follow", actors.iri("c"), inboxTestBlogIRI)
	r = newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.iri("c"), key)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)
}

func TestInboxRefusesActorMismatch(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	keyA := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &keyA.PublicKey})
	actors.set("b", &inboxTestActor{key: &inboxTestKey(t, 1).PublicKey})

	// A Follow by B, signed by A
	body := inboxActivity(t, "Follow", actors.iri("b"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), keyA)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)

	// A's Undo of a Like by B
	like := map[string]any{"type": "Like", "actor": actors.iri("b"), "object": inboxTestBlogIRI + "/posts/abc"}
	body = inboxActivity(t, "Undo", actors.iri("a"), like)
	r = newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), keyA)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)

	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxRefusesBadDigest(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	signed := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	sent := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI+"/other")
	r := newInboxRequest(t, sent)
	signInboxRequest(t, r, signed, actors.keyID("a"), key)
	rec := serveInbox(t, app, r)

	assertInboxStatus(t, rec, http.StatusUnauthorized)
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxRefusesStaleDate(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	r.Header.Set("Date", time.Now().Add(-13*time.Hour).UTC().Format(http.TimeFormat))
	signInboxRequest(t, r, body, actors.keyID("a"), key)
	rec := serveInbox(t, app, r)

	assertInboxStatus(t, rec, http.StatusUnauthorized)
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxAcceptsSignedFollow(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), key)
	rec := serveInbox(t, app, r)
	assertInboxStatus(t, rec, http.StatusOK)

	// The follower is stored once the Accept is delivered, a moment later
	const query = "SELECT COUNT(*) FROM remotefollows f INNER JOIN remoteusers u ON u.id = f.remote_user_id WHERE u.actor_id = ?"
	deadline := time.Now().Add(15 * time.Second)
	for inboxRowCount(t, app, query, actors.iri("a")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no follower row for %s", actors.iri("a"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestInboxRefetchesRotatedKey(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	oldKey, newKey := inboxTestKey(t, 0), inboxTestKey(t, 1)
	actors.set("a", &inboxTestActor{key: &newKey.PublicKey})
	addInboxTestFollower(t, app, actors.iri("a"), actors.keyID("a"), &oldKey.PublicKey)

	// The second request finds the new key cached
	for range 2 {
		body := inboxActivity(t, "Like", actors.iri("a"), inboxTestBlogIRI+"/posts/abc")
		r := newInboxRequest(t, body)
		signInboxRequest(t, r, body, actors.keyID("a"), newKey)
		signer, err := verifyInboxRequest(app, r, body)
		if err != nil || signer != actors.iri("a") {
			t.Fatalf("verifyInboxRequest = %q, %v; want %q, nil", signer, err, actors.iri("a"))
		}
	}
	if n := actors.gets.Load(); n != 1 {
		t.Errorf("actor server got %d GETs, want 1", n)
	}
}

func TestInboxRefetchesStoredKeyOncePerInterval(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key, otherKey := inboxTestKey(t, 0), inboxTestKey(t, 1)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})
	addInboxTestFollower(t, app, actors.iri("a"), actors.keyID("a"), &key.PublicKey)
	now := time.Now()
	app.inboxKeyRefetches().now = func() time.Time { return now }

	verify := func(signingKey *rsa.PrivateKey) error {
		body := inboxActivity(t, "Like", actors.iri("a"), inboxTestBlogIRI+"/posts/abc")
		r := newInboxRequest(t, body)
		signInboxRequest(t, r, body, actors.keyID("a"), signingKey)
		_, err := verifyInboxRequest(app, r, body)
		return err
	}

	// The stored key verifies neither, so the first fetches the actor again
	// and the second, within the interval, doesn't
	for i := range 2 {
		if err := verify(otherKey); !errors.Is(err, errBadSignature) {
			t.Fatalf("request %d with another key: verifyInboxRequest error = %v, want errBadSignature", i+1, err)
		}
	}
	if n := actors.gets.Load(); n != 1 {
		t.Errorf("after two requests with another key, actor server got %d GETs, want 1", n)
	}

	// The stored key is still tried
	if err := verify(key); err != nil {
		t.Errorf("request with the stored key: verifyInboxRequest: %v", err)
	}
	if n := actors.gets.Load(); n != 1 {
		t.Errorf("after a request with the stored key, actor server got %d GETs, want 1", n)
	}

	// Once the interval has passed, the actor is fetched again
	now = now.Add(keyRefetchInterval)
	if err := verify(otherKey); !errors.Is(err, errBadSignature) {
		t.Fatalf("request with another key after the interval: verifyInboxRequest error = %v, want errBadSignature", err)
	}
	if n := actors.gets.Load(); n != 2 {
		t.Errorf("after the interval, actor server got %d GETs, want 2", n)
	}
}

func TestInboxDropsDeleteFromGoneActor(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("gone", &inboxTestActor{status: http.StatusGone})
	addInboxTestFollower(t, app, actors.iri("gone"), "", nil)

	body := inboxActivity(t, "Delete", actors.iri("gone"), actors.iri("gone"))
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("gone"), key)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusAccepted)

	// Any other activity from the gone actor can't be verified
	body = inboxActivity(t, "Follow", actors.iri("gone"), inboxTestBlogIRI)
	r = newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("gone"), key)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)

	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remoteusers"); n != 1 {
		t.Errorf("%d remote users, want 1", n)
	}
	if n := followerCount(t, app); n != 1 {
		t.Errorf("%d followers, want 1", n)
	}
	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remoteuserkeys"); n != 0 {
		t.Errorf("%d remote user keys, want 0", n)
	}
}

func TestVerifyInboxRequestKeyOwner(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})
	actors.set("c", &inboxTestActor{key: &key.PublicKey, owner: "https://elsewhere.test/users/c"})

	// A key at an IRI of its own, owned by an actor on the same host, and
	// labelled hs2019
	body := inboxActivity(t, "Like", actors.iri("a"), inboxTestBlogIRI+"/posts/abc")
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.iri("a")+"/main-key", key)
	r.Header.Set("Signature", strings.Replace(r.Header.Get("Signature"), `algorithm="rsa-sha256"`, `algorithm="hs2019"`, 1))
	if signer, err := verifyInboxRequest(app, r, body); err != nil || signer != actors.iri("a") {
		t.Errorf("key at its own IRI: verifyInboxRequest = %q, %v; want %q, nil", signer, err, actors.iri("a"))
	}

	// A key whose owner is on another host
	body = inboxActivity(t, "Like", "https://elsewhere.test/users/c", inboxTestBlogIRI+"/posts/abc")
	r = newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("c"), key)
	if signer, err := verifyInboxRequest(app, r, body); !errors.Is(err, errBadSignature) {
		t.Errorf("owner on another host: verifyInboxRequest = %q, %v; want errBadSignature", signer, err)
	}
}

func TestVerifyInboxRequestRefusesWeakSignatures(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	cases := []struct {
		name    string
		algo    httpsig.Algorithm
		headers []string
	}{
		{"rsa-sha1", httpsig.RSASHA1, []string{"(request-target)", "host", "date", "digest"}},
		{"digest not signed", httpsig.RSASHA256, []string{"(request-target)", "host", "date"}},
		{"date not signed", httpsig.RSASHA256, []string{"(request-target)", "host", "digest"}},
		{"target not signed", httpsig.RSASHA256, []string{"host", "date", "digest"}},
		{"host not signed", httpsig.RSASHA256, []string{"(request-target)", "date", "digest"}},
	}
	for _, c := range cases {
		body := inboxActivity(t, "Like", actors.iri("a"), inboxTestBlogIRI+"/posts/abc")
		r := newInboxRequest(t, body)
		sum := sha256.Sum256(body)
		r.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]))
		r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		if err := httpsig.NewSigner(actors.keyID("a"), key, c.algo, c.headers).SignSigHeader(r); err != nil {
			t.Fatalf("%s: sign: %v", c.name, err)
		}
		if signer, err := verifyInboxRequest(app, r, body); !errors.Is(err, errBadSignature) {
			t.Errorf("%s: verifyInboxRequest = %q, %v; want errBadSignature", c.name, signer, err)
		}
	}
}

// assertNoRemoteUsers fails the test if any remote user or key is stored.
func assertNoRemoteUsers(t *testing.T, app *App) {
	t.Helper()
	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remoteusers"); n != 0 {
		t.Errorf("%d remote users stored, want 0", n)
	}
	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remoteuserkeys"); n != 0 {
		t.Errorf("%d remote user keys stored, want 0", n)
	}
}

// likeFromHonestThenForeign sends a Like signed by name's key, which the
// GET that verifies it finds in name's own document. getActor's GETs
// after it get the document as actors.set made it.
func likeFromHonestThenForeign(t *testing.T, app *App, actors *inboxTestActors, name string, key *rsa.PrivateKey) {
	t.Helper()
	body := inboxActivity(t, "Like", actors.iri(name), "https://writefreely.test/api/posts/abc")
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID(name), key)
	serveInbox(t, app, r)
}

func TestGetActorRefusesForeignID(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)

	actors.set("a", &inboxTestActor{key: &key.PublicKey, id: "https://elsewhere.test/users/a"})
	if actor, _, err := getActor(app, actors.iri("a")); err == nil {
		t.Errorf("getActor(%s) = actor %s, want an error", actors.iri("a"), actor.ID)
	}

	actors.set("b", &inboxTestActor{key: &key.PublicKey, id: "https://elsewhere.test/users/b", honestGets: 1})
	likeFromHonestThenForeign(t, app, actors, "b", key)
	assertNoRemoteUsers(t, app)
}

func TestGetActorRefusesForeignKey(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	other := newInboxTestActors(t)
	key := inboxTestKey(t, 0)

	// A key id on another host
	actors.set("a", &inboxTestActor{key: &key.PublicKey, keyID: "https://elsewhere.test/users/a#main-key"})
	if actor, _, err := getActor(app, actors.iri("a")); err == nil {
		t.Errorf("key id on another host: getActor(%s) = actor %s, want an error", actors.iri("a"), actor.ID)
	}

	// A key owner on another host, which serves that owner
	other.set("o", &inboxTestActor{key: &key.PublicKey})
	actors.set("o", &inboxTestActor{key: &key.PublicKey, owner: other.iri("o")})
	if actor, _, err := getActor(app, actors.iri("o")); err == nil {
		t.Errorf("key owner on another host: getActor(%s) = actor %s, want an error", actors.iri("o"), actor.ID)
	}

	actors.set("b", &inboxTestActor{key: &key.PublicKey, keyID: "https://elsewhere.test/users/b#main-key", honestGets: 1})
	likeFromHonestThenForeign(t, app, actors, "b", key)
	assertNoRemoteUsers(t, app)
}

func TestInboxRefusesKeyClaimedForAnotherHost(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	x, y := newInboxTestActors(t), newInboxTestActors(t)
	xKey, yKey := inboxTestKey(t, 0), inboxTestKey(t, 1)
	y.set("v", &inboxTestActor{key: &yKey.PublicKey})

	// X's account verifies as itself, then X serves a document that
	// claims Y's account and key, with X's key
	x.set("evil", &inboxTestActor{key: &xKey.PublicKey, id: y.iri("v"), keyID: y.keyID("v"), honestGets: 1})
	likeFromHonestThenForeign(t, app, x, "evil", xKey)

	// X's key, claiming Y's account
	body := inboxActivity(t, "Like", y.iri("v"), "https://writefreely.test/api/posts/def")
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, y.keyID("v"), xKey)
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)

	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remoteusers WHERE actor_id = ?", y.iri("v")); n != 0 {
		t.Errorf("%d remote users stored for %s, want 0", n, y.iri("v"))
	}
}

func TestFetchRemoteActorComparesRedirectTarget(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	actors.set("a", &inboxTestActor{key: &inboxTestKey(t, 0).PublicKey})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, actors.srv.URL+r.URL.Path, http.StatusFound) //nolint:gosec // a test server redirecting to the test actor server
	}))
	t.Cleanup(front.Close)
	allowTestServerDial(t, front)

	// The document names the host it was redirected to, which served it
	actor, err := fetchRemoteActor(app, front.URL+"/users/a")
	if err != nil {
		t.Fatalf("fetchRemoteActor through a redirect: %v", err)
	}
	if actor.ID != actors.iri("a") {
		t.Errorf("id = %q, want %q", actor.ID, actors.iri("a"))
	}
}

func TestInboxRefusesRedirectToAnotherActor(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	x, y := newInboxTestActors(t), newInboxTestActors(t)
	xKey := inboxTestKey(t, 0)
	y.set("v", &inboxTestActor{key: &inboxTestKey(t, 1).PublicKey})

	// X's accounts verify as themselves, then X redirects the GETs after
	// that to Y's account
	for _, typ := range []string{"Like", "Follow"} {
		name := "evil-" + strings.ToLower(typ)
		x.set(name, &inboxTestActor{key: &xKey.PublicKey, honestGets: 1, redirect: y.iri("v")})
		object := "https://writefreely.test/api/posts/abc"
		if typ == "Follow" {
			object = inboxTestBlogIRI
		}
		body := inboxActivity(t, typ, x.iri(name), object)
		r := newInboxRequest(t, body)
		signInboxRequest(t, r, body, x.keyID(name), xKey)
		if rec := serveInbox(t, app, r); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: inbox answered %d %s, want 401", typ, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}

	assertNoRemoteUsers(t, app)
	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remote_likes"); n != 0 {
		t.Errorf("%d likes stored, want 0", n)
	}
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxRefusesSignatureForAnotherHost(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	body := inboxActivity(t, "Follow", actors.iri("a"), inboxTestBlogIRI)
	r := newInboxRequest(t, body)
	r.Host = "other.example"
	signInboxRequest(t, r, body, actors.keyID("a"), key)

	assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)
	if n := followerCount(t, app); n != 0 {
		t.Errorf("%d followers, want 0", n)
	}
}

func TestInboxVerifiesAgainstConfiguredHost(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})

	// Signed for this instance's host, and received with the Host a proxy
	// in front rewrote it to
	body := inboxActivity(t, "Like", actors.iri("a"), "https://writefreely.test/api/posts/abc")
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), key)
	r.Host = "127.0.0.1:8080"
	assertInboxStatus(t, serveInbox(t, app, r), http.StatusOK)
	if n := inboxRowCount(t, app, "SELECT COUNT(*) FROM remote_likes"); n != 1 {
		t.Errorf("%d likes stored, want 1", n)
	}

	// The configured host is compared lowercased and without the scheme's
	// default port; with another port, the bare host name is accepted too,
	// as servers that sign without a port send it
	for _, c := range []struct {
		configured string
		signed     []string
	}{
		{"https://Blog.Example", []string{"blog.example"}},
		{"https://blog.example:443", []string{"blog.example"}},
		{"http://blog.example:80", []string{"blog.example"}},
		{"https://blog.example:8443", []string{"blog.example:8443", "blog.example"}},
	} {
		app.cfg.App.Host = c.configured
		for _, host := range append(c.signed, "other.example") {
			want := host != "other.example"
			body := inboxActivity(t, "Like", actors.iri("a"), "https://writefreely.test/api/posts/def")
			r := newInboxRequest(t, body)
			r.Host = host
			signInboxRequest(t, r, body, actors.keyID("a"), key)
			r.Host = "127.0.0.1:8080"
			if _, err := verifyInboxRequest(app, r, body); (err == nil) != want {
				t.Errorf("[app] host %s, signed for %s: verifyInboxRequest error %v, want verified %v", c.configured, host, err, want)
			}
		}
	}
}

// lockedBuffer is a bytes.Buffer that loggers on several goroutines may
// write to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestInboxRefusesWhenConfiguredHostUnusable(t *testing.T) { //nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables, and the error log is shared
	app := newInboxTestApp(t)
	actors := newInboxTestActors(t)
	key := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &key.PublicKey})
	errLog := &lockedBuffer{}
	prev := log.ErrorLog.Writer()
	log.ErrorLog.SetOutput(io.MultiWriter(prev, errLog))
	t.Cleanup(func() { log.ErrorLog.SetOutput(prev) })

	// A host without a scheme parses as a path, with no host name. It is
	// logged once per process, so forget any earlier run of this test.
	app.cfg.App.Host = "blog.example"
	inboxHostsLogged.Delete(app.cfg.App.Host)
	for range 2 {
		body := inboxActivity(t, "Like", actors.iri("a"), "https://writefreely.test/api/posts/abc")
		r := newInboxRequest(t, body)
		r.Host = "blog.example"
		signInboxRequest(t, r, body, actors.keyID("a"), key)
		assertInboxStatus(t, serveInbox(t, app, r), http.StatusUnauthorized)
	}

	if n := strings.Count(errLog.String(), `[app] host "blog.example"`); n != 1 {
		t.Errorf("error log names [app] host %d times, want once:\n%s", n, errLog)
	}
	if n := actors.gets.Load(); n != 0 {
		t.Errorf("actor server got %d GETs, want 0", n)
	}
}
