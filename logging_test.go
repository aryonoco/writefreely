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
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/writeas/web-core/log"
	"github.com/writefreely/writefreely/config"
	"github.com/writefreely/writefreely/key"
)

func TestReqLogDropsQuery(t *testing.T) {
	t.Parallel()
	app := &App{cfg: config.New()}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/reset?t=SENTINEL", http.NoBody)

	line := app.ReqLog(r, http.StatusOK, time.Millisecond)
	if !strings.Contains(line, "/reset") || strings.Contains(line, "SENTINEL") {
		t.Errorf("request log line %q, want the path /reset without its query", line)
	}
}

// captureLog sends everything web-core logs to the returned buffer until
// the test ends.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	out := &lockedBuffer{}
	infoOut, errorOut := log.InfoLog.Writer(), log.ErrorLog.Writer()
	log.InfoLog.SetOutput(out)
	log.ErrorLog.SetOutput(out)
	t.Cleanup(func() {
		log.InfoLog.SetOutput(infoOut)
		log.ErrorLog.SetOutput(errorOut)
	})
	return out
}

// logTestPost adds a post to the blog at inboxTestAlias and returns it as
// the publish jobs pass it on.
func logTestPost(t *testing.T, app *App) *PublicPost {
	t.Helper()
	c, err := app.db.GetCollection(inboxTestAlias)
	if err != nil {
		t.Fatalf("get blog: %v", err)
	}
	title, content := "A post", "Some words for the followers."
	post, err := app.db.CreatePost(c.OwnerID, c.ID, &SubmittedPost{Title: &title, Content: &content})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	p, err := app.db.GetPost(post.ID, 0)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	c.hostName = app.cfg.App.Host
	c.ForPublic()
	p.Collection = &CollectionObj{Collection: *c}
	return p
}

// refusingSMTPServer is a plain SMTP server that refuses every recipient,
// repeating the address in its reply as Postfix does, and records the
// recipients it was given.
type refusingSMTPServer struct {
	ln net.Listener

	mu    sync.Mutex
	rcpts []string
	conns []net.Conn
}

// newRefusingSMTPServer starts a refusingSMTPServer on a loopback address
// until the test ends, and returns it with its port.
func newRefusingSMTPServer(t *testing.T) (s *refusingSMTPServer, port int) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s = &refusingSMTPServer{ln: ln}
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	go s.serve()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", ln.Addr())
	}
	return s, tcpAddr.Port
}

func (s *refusingSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *refusingSMTPServer) handle(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 localhost ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch strings.ToUpper(strings.Fields(line + " x")[0]) {
		case "EHLO", "HELO":
			reply("250 localhost")
		case "RCPT":
			s.mu.Lock()
			s.rcpts = append(s.rcpts, line)
			s.mu.Unlock()
			addr, _, _ := strings.Cut(strings.TrimPrefix(line[len("RCPT TO:"):], "<"), ">")
			reply("550 5.1.1 <" + addr + ">: Recipient address rejected: User unknown")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 OK")
		}
	}
}

func (s *refusingSMTPServer) recipients() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.rcpts, "\n")
}

//nolint:paralleltest // swaps web-core's package-level loggers; newFetchTestApp and allowTestServerDial set package-level variables
func TestNoSensitiveLogLines(t *testing.T) {
	out := captureLog(t)

	// A login with a name that matches no account
	loginApp := newSignupTestApp(t)
	router := InitRoutes(loginApp, mux.NewRouter())
	_, flashes := webLoginFlashes(t, loginApp, router, "198.51.100.1:1000", "SENTINEL-NAME", "a-password")
	if len(flashes) != 1 || flashes[0] != "Incorrect username or password." {
		t.Errorf("login flashes %v, want [Incorrect username or password.]", flashes)
	}

	// A login with a name too long to keep in the session cookie
	webLoginFlashes(t, loginApp, router, "198.51.100.2:1000", "SENTINEL-LONGNAME-"+strings.Repeat("x", 4096), "a-password")
	if !strings.Contains(out.String(), "Couldn't saveTempInfo") {
		t.Errorf("the long name didn't fail to save:\n%s", out)
	}

	// Requests with a token in their query string, to a page and to a
	// plain-text endpoint
	rec := serveCSRFTest(router, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/reset?t=SENTINEL-TOKEN", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /reset with an unknown token: %d, want 404", rec.Code)
	}
	plain := NewHandler(loginApp).PlainTextAPI(func(*App, http.ResponseWriter, *http.Request) error { return nil })
	plain(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/collections/x/monetization-pointer?t=SENTINEL-TOKEN", http.NoBody))

	// A signed inbox delivery whose body isn't JSON. Its answer is the
	// error page, which looks up the session.
	app := newInboxTestApp(t)
	app.keys = &key.Keychain{}
	if err := app.keys.GenerateKeys(); err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	app.InitSession()
	actors := newInboxTestActors(t)
	signKey := inboxTestKey(t, 0)
	actors.set("a", &inboxTestActor{key: &signKey.PublicKey})
	body := []byte("SENTINEL-BODY")
	r := newInboxRequest(t, body)
	signInboxRequest(t, r, body, actors.keyID("a"), signKey)
	serveInbox(t, app, r)
	if !strings.Contains(out.String(), "Failed decoding JSON") {
		t.Errorf("the inbox didn't reach decoding the body:\n%s", out)
	}

	// A post federated to two followers, who share an inbox
	var delivered atomic.Int32
	inbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delivered.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(inbox.Close)
	allowTestServerDial(t, inbox)
	c, err := app.db.GetCollection(inboxTestAlias)
	if err != nil {
		t.Fatalf("get blog: %v", err)
	}
	for _, name := range []string{"SENTINEL-FOLLOWER-A", "SENTINEL-FOLLOWER-B"} {
		res, err := app.db.ExecContext(t.Context(), "INSERT INTO remoteusers (actor_id, inbox, shared_inbox) VALUES (?, ?, ?)",
			inbox.URL+"/users/"+name, inbox.URL+"/inbox", inbox.URL+"/inbox")
		if err != nil {
			t.Fatalf("add remote user: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("remote user id: %v", err)
		}
		if _, err := app.db.ExecContext(t.Context(), "INSERT INTO remotefollows (collection_id, remote_user_id, created) VALUES (?, ?, "+app.db.now()+")", c.ID, id); err != nil {
			t.Fatalf("add follow: %v", err)
		}
	}
	p := logTestPost(t, app)
	if err := federatePost(app, p, p.Collection.ID, false); err != nil {
		t.Errorf("federatePost: %v", err)
	}
	if n := delivered.Load(); n != 1 {
		t.Errorf("the followers' inbox got %d deliveries, want 1", n)
	}

	// A post emailed to a subscriber, whose server refuses it
	smtp, port := newRefusingSMTPServer(t)
	app.cfg.Email = config.EmailCfg{Host: "127.0.0.1", Port: port, Username: "writefreely", Password: "unused", Domain: "writefreely.test"}
	if _, err := app.db.AddEmailSubscription(c.ID, 0, "sentinel@example.test", true); err != nil {
		t.Fatalf("add subscriber: %v", err)
	}
	if err := emailPost(app, logTestPost(t, app), c.ID); err == nil {
		t.Error("emailPost to a refused recipient succeeded")
	}
	if !strings.Contains(smtp.recipients(), "sentinel@example.test") {
		t.Errorf("the mail server wasn't given the subscriber; it got %q", smtp.recipients())
	}
	if !strings.Contains(out.String(), "answered 550") {
		t.Errorf("the log doesn't give the mail server's reply code:\n%s", out)
	}

	logged := out.String()
	var carried []string
	for _, s := range []string{"SENTINEL-NAME", "SENTINEL-LONGNAME", "SENTINEL-TOKEN", "SENTINEL-BODY", "SENTINEL-FOLLOWER", "sentinel@example.test"} {
		if strings.Contains(logged, s) {
			carried = append(carried, s)
		}
	}
	if len(carried) > 0 {
		t.Errorf("the log carries %s:\n%s", strings.Join(carried, ", "), logged)
	}
}

// paddedInboxDelete returns a Delete by actor, as JSON of exactly size
// bytes.
func paddedInboxDelete(t *testing.T, actor string, size int) []byte {
	t.Helper()
	activity := func(pad int) []byte {
		body, err := json.Marshal(map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       actor + "#delete",
			"type":     "Delete",
			"actor":    actor,
			"object":   actor + "/notes/1",
			"summary":  strings.Repeat("x", pad),
		})
		if err != nil {
			t.Fatalf("marshal Delete: %v", err)
		}
		return body
	}
	body := activity(size - len(activity(0)))
	if len(body) != size {
		t.Fatalf("padded Delete is %d bytes, want %d", len(body), size)
	}
	return body
}

// TestInboxBodyCap sends Deletes over the cap, signed ones that the inbox
// would otherwise accept and unsigned ones, and unsigned ones at the cap,
// which go on to be refused for their signature. An accepted activity
// would start work that outlives the test.
//
//nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
func TestInboxBodyCap(t *testing.T) {
	cases := []struct {
		name   string
		limit  int64
		size   int
		signed bool
		want   int
	}{
		{"inbox_max_bytes = 1024, signed 2 KiB", 1024, 2048, true, http.StatusRequestEntityTooLarge},
		{"inbox_max_bytes = 1024, unsigned 2 KiB", 1024, 2048, false, http.StatusRequestEntityTooLarge},
		{"inbox_max_bytes = 1024, unsigned 1 KiB", 1024, 1024, false, http.StatusUnauthorized},
		{"default, signed 1 MiB + 1", 0, 1<<20 + 1, true, http.StatusRequestEntityTooLarge},
		{"default, unsigned 1 MiB + 1", 0, 1<<20 + 1, false, http.StatusRequestEntityTooLarge},
		{"default, unsigned 1 MiB", 0, 1 << 20, false, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newInboxTestApp(t)
			app.cfg.Server.InboxMaxBytes = tc.limit
			actors := newInboxTestActors(t)
			signKey := inboxTestKey(t, 0)
			actors.set("a", &inboxTestActor{key: &signKey.PublicKey})

			body := paddedInboxDelete(t, actors.iri("a"), tc.size)
			r := newInboxRequest(t, body)
			if tc.signed {
				signInboxRequest(t, r, body, actors.keyID("a"), signKey)
			}
			// Not assertInboxStatus, which prints the body: an accepted
			// Delete is answered with itself
			if rec := serveInbox(t, app, r); rec.Code != tc.want {
				t.Errorf("a %d-byte body: inbox answered %d, want %d", tc.size, rec.Code, tc.want)
			}
			if n := actors.gets.Load(); tc.want == http.StatusRequestEntityTooLarge && n != 0 {
				t.Errorf("actor server got %d GETs for a body over the cap, want 0", n)
			}
		})
	}
}
