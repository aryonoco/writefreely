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
	"database/sql"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/writefreely/writefreely/config"
)

// newGopherTestApp returns an App backed by a temporary SQLite database,
// with Gopher set to a free port and mutate applied to its config.
func newGopherTestApp(t *testing.T, mutate func(cfg *config.Config)) *App {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "writefreely.db")
	db, err := sql.Open("sqlite3_with_regex", dbPath+"?parseTime=true&cached=shared")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.New()
	cfg.UseSQLite(true)
	cfg.Database.FileName = dbPath
	cfg.App.Host = "https://blog.example.com"
	cfg.App.SiteName = "Gopher Test"
	cfg.Server.GopherPort = freeTCPPort(t)
	if mutate != nil {
		mutate(cfg)
	}

	app := &App{
		db:  &datastore{DB: db, driverName: driverSQLite},
		cfg: cfg,
	}
	if err := adminInitDatabase(app); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return app
}

// freeTCPPort returns a TCP port that nothing was listening on a moment ago.
func freeTCPPort(t *testing.T) int {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release port %d: %v", port, err)
	}
	return port
}

// createGopherTestBlog creates a user whose blog has the given alias and
// visibility and holds one post, and returns that post.
func createGopherTestBlog(t *testing.T, app *App, alias string, vis collVisibility, title, content string) *Post {
	t.Helper()

	u := &User{Username: alias, HashedPass: []byte("unused")}
	if err := app.db.CreateUser(app.cfg, u, "", ""); err != nil {
		t.Fatalf("create user %q: %v", alias, err)
	}
	c, err := app.db.GetCollection(alias)
	if err != nil {
		t.Fatalf("get blog %q: %v", alias, err)
	}
	_, err = app.db.ExecContext(t.Context(), "UPDATE collections SET privacy = ? WHERE id = ?", int(vis), c.ID)
	if err != nil {
		t.Fatalf("set visibility of %q: %v", alias, err)
	}

	// An hour old, so that it is not a post scheduled for later
	created := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05Z")
	p, err := app.db.CreatePost(u.ID, c.ID, &SubmittedPost{Title: &title, Content: &content, Created: &created})
	if err != nil {
		t.Fatalf("create post in %q: %v", alias, err)
	}
	return p
}

// startTestGopher starts Gopher for app and returns the address to dial.
func startTestGopher(t *testing.T, app *App) string {
	t.Helper()

	l, err := startGopher(app)
	if err != nil {
		t.Fatalf("start gopher: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(app.cfg.Server.GopherPort))
}

func gopherDial(t *testing.T, addr string) net.Conn {
	t.Helper()

	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	return c
}

// holdGopherConns opens n connections to addr that send nothing yet, and
// closes them when the test ends.
func holdGopherConns(t *testing.T, addr string, n int) []net.Conn {
	t.Helper()

	conns := make([]net.Conn, 0, n)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for range n {
		conns = append(conns, gopherDial(t, addr))
	}
	return conns
}

// gopherRequest sends selector on c and returns the whole response.
func gopherRequest(t *testing.T, c net.Conn, selector string) string {
	t.Helper()

	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.WriteString(c, selector+"\r\n"); err != nil {
		t.Fatalf("send %q: %v", selector, err)
	}
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read the response to %q: %v", selector, err)
	}
	return string(b)
}

// gopherFetch requests selector on a new connection to addr.
func gopherFetch(t *testing.T, addr, selector string) string {
	t.Helper()

	c := gopherDial(t, addr)
	defer func() { _ = c.Close() }()
	return gopherRequest(t, c, selector)
}

// checkGopherLinks fails t unless menu holds at least one file or
// directory item, and every such item ends with wantSuffix.
func checkGopherLinks(t *testing.T, menu, wantSuffix string) {
	t.Helper()

	links := 0
	for line := range strings.SplitSeq(menu, "\r\n") {
		if !strings.HasPrefix(line, "0") && !strings.HasPrefix(line, "1") {
			continue
		}
		links++
		if !strings.HasSuffix(line, wantSuffix) {
			t.Errorf("link %q does not end with %q", line, wantSuffix)
		}
	}
	if links == 0 {
		t.Errorf("menu holds no links:\n%s", menu)
	}
}

func TestGopherLinksAdvertisePort(t *testing.T) {
	t.Parallel()

	t.Run("SingleUser", func(t *testing.T) {
		t.Parallel()

		app := newGopherTestApp(t, func(cfg *config.Config) {
			cfg.App.SingleUser = true
			cfg.Server.GopherAdvertisedPort = 70
		})
		createGopherTestBlog(t, app, "gophertest", CollPublic, "Hello", "A public post.")
		addr := startTestGopher(t, app)

		menu := gopherFetch(t, addr, "")
		if !strings.Contains(menu, "\r\n0") {
			t.Fatalf("root menu lists no post:\n%s", menu)
		}
		checkGopherLinks(t, menu, "\tblog.example.com\t70")
	})

	t.Run("MultiUser", func(t *testing.T) {
		t.Parallel()

		app := newGopherTestApp(t, func(cfg *config.Config) {
			cfg.App.SingleUser = false
			cfg.Server.GopherAdvertisedPort = 70
		})
		createGopherTestBlog(t, app, "gophertest", CollPublic, "Hello", "A public post.")
		addr := startTestGopher(t, app)

		checkGopherLinks(t, gopherFetch(t, addr, ""), "\tblog.example.com\t70")
		checkGopherLinks(t, gopherFetch(t, addr, "/gophertest/"), "\tblog.example.com\t70")
	})
}

func TestGopherDefaultAdvertisedPort(t *testing.T) {
	t.Parallel()

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = true
	})
	createGopherTestBlog(t, app, "gophertest", CollPublic, "Hello", "A public post.")
	addr := startTestGopher(t, app)

	checkGopherLinks(t, gopherFetch(t, addr, ""), "\tblog.example.com\t"+strconv.Itoa(app.cfg.Server.GopherPort))
}

func TestGopherHidesPrivateBlog(t *testing.T) {
	t.Parallel()

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
	})
	createGopherTestBlog(t, app, "openblog", CollPublic, "Open post", "Anyone may read this.")
	hidden := []struct {
		alias, title, content string
		vis                   collVisibility
	}{
		{"privateblog", "Private post", "Only the owner may read this.", CollPrivate},
		{"lockedblog", "Locked post", "Only readers with the password may read this.", CollProtected},
	}
	slugs := make([]string, len(hidden))
	for i, h := range hidden {
		slugs[i] = createGopherTestBlog(t, app, h.alias, h.vis, h.title, h.content).Slug.String
	}
	addr := startTestGopher(t, app)

	root := gopherFetch(t, addr, "")
	if !strings.Contains(root, "/openblog/") {
		t.Errorf("root menu does not list the public blog:\n%s", root)
	}
	missing := gopherFetch(t, addr, "/nosuchblog/")
	if !strings.HasPrefix(missing, "3") {
		t.Fatalf("a missing blog answers %q, not an error item", missing)
	}

	for i, h := range hidden {
		if strings.Contains(root, h.alias) {
			t.Errorf("root menu lists %s:\n%s", h.alias, root)
		}
		for _, sel := range []string{"/" + h.alias + "/", "/" + h.alias + "/" + slugs[i]} {
			got := gopherFetch(t, addr, sel)
			if strings.Contains(got, h.title) || strings.Contains(got, h.content) {
				t.Errorf("%s serves the post:\n%s", sel, got)
			}
			if got != missing {
				t.Errorf("%s answers %q, not the error item a missing blog gets (%q)", sel, got, missing)
			}
		}
	}
}

func TestGopherTextFraming(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	if err := writeGopherText(&b, "a\n.\n.b\nc"); err != nil {
		t.Fatalf("writeGopherText: %v", err)
	}
	if want := "a\r\n..\r\n..b\r\nc\r\n.\r\n"; b.String() != want {
		t.Errorf("writeGopherText wrote %q, want %q", b.String(), want)
	}

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = true
	})
	p := createGopherTestBlog(t, app, "gophertest", CollPublic, "", "a\n.\n.b\nc")
	addr := startTestGopher(t, app)

	got := gopherFetch(t, addr, "/"+p.Slug.String)
	if want := "\r\n\r\na\r\n..\r\n..b\r\nc\r\n.\r\n"; !strings.HasSuffix(got, want) {
		t.Errorf("post response %q does not end with %q", got, want)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("post response %q has a line not ended by CRLF", got)
	}
}

func TestStartGopherBindFailure(t *testing.T) {
	t.Parallel()

	taken, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatalf("bind a port: %v", err)
	}
	t.Cleanup(func() { _ = taken.Close() })
	port := taken.Addr().(*net.TCPAddr).Port

	cfg := config.New()
	cfg.Server.GopherPort = port
	l, err := startGopher(&App{cfg: cfg})
	if err == nil {
		_ = l.Close()
		t.Fatalf("startGopher bound port %d, which is already in use", port)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Errorf("error %q does not name port %d", err, port)
	}
}

func TestGopherReadDeadline(t *testing.T) {
	t.Parallel()

	app := newGopherTestApp(t, nil)
	addr := startTestGopher(t, app)

	c := gopherDial(t, addr)
	defer func() { _ = c.Close() }()
	start := time.Now()
	if err := c.SetReadDeadline(start.Add(11 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.Copy(io.Discard, c); err != nil {
		t.Fatalf("connection that sent no selector still open after %v: %v", time.Since(start).Round(time.Second), err)
	}
}

func TestGopherConnectionCap(t *testing.T) {
	t.Parallel()

	const held = 64
	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
	})
	addr := startTestGopher(t, app)

	idle := holdGopherConns(t, addr, held-1)
	// One connection short of the cap, a request is still answered.
	if got := gopherFetch(t, addr, ""); !strings.HasSuffix(got, ".\r\n") {
		t.Fatalf("with %d connections held, the root menu is %q", held-1, got)
	}
	idle = append(idle, holdGopherConns(t, addr, 1)...)

	// At the cap, the next connection gets no answer...
	extra := gopherDial(t, addr)
	defer func() { _ = extra.Close() }()
	if _, err := io.WriteString(extra, "\r\n"); err != nil {
		t.Fatalf("send selector: %v", err)
	}
	if err := extra.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var ne net.Error
	if n, err := extra.Read(make([]byte, 1)); !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("with %d connections held, another got an answer (%d bytes, %v)", held, n, err)
	}

	// ...until one closes.
	if err := idle[0].Close(); err != nil {
		t.Fatalf("close a held connection: %v", err)
	}
	if err := extra.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	got, err := io.ReadAll(extra)
	if err != nil || !strings.HasSuffix(string(got), ".\r\n") {
		t.Fatalf("after a held connection closed, the waiting one got %q (%v)", got, err)
	}

	// Finish the held requests, so that no server work outlives the test.
	for _, c := range idle[1:] {
		gopherRequest(t, c, "")
	}
}

func TestGopherConnectionCapAfterResets(t *testing.T) {
	t.Parallel()

	const held = 64
	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
	})
	p := createGopherTestBlog(t, app, "gophertest", CollPublic, "Hello", "A public post.")
	addr := startTestGopher(t, app)

	// Clients that reset the connection right after sending their selector,
	// so that writing the answer fails
	for range 2 * held {
		c := gopherDial(t, addr)
		if _, err := io.WriteString(c, "/gophertest/"+p.Slug.String+"\r\n"); err != nil {
			t.Fatalf("send selector: %v", err)
		}
		if err := c.(*net.TCPConn).SetLinger(0); err != nil {
			t.Fatalf("set linger: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("reset connection: %v", err)
		}
	}

	// Every place is free again: one connection short of the cap, a request
	// is answered.
	idle := holdGopherConns(t, addr, held-1)
	if got := gopherFetch(t, addr, ""); !strings.HasSuffix(got, ".\r\n") {
		t.Fatalf("after %d resets, with %d connections held, the root menu is %q", 2*held, held-1, got)
	}
	for _, c := range idle {
		gopherRequest(t, c, "")
	}
}

// TestGopherExchangeDeadline does not run in parallel, because it shortens
// gopherExchangeTimeout, which startGopher reads.
//
//nolint:paralleltest // It changes gopherExchangeTimeout, a package variable.
func TestGopherExchangeDeadline(t *testing.T) {
	const held = 64
	prev := gopherExchangeTimeout
	gopherExchangeTimeout = time.Second
	t.Cleanup(func() { gopherExchangeTimeout = prev })

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
	})
	// Far more than the socket buffers hold, so that writing it blocks
	// while the client does not read
	doc := strings.Repeat("x", 16<<20)
	p := createGopherTestBlog(t, app, "gophertest", CollPublic, "Big", doc)
	addr := startTestGopher(t, app)

	// Fill the cap with connections that send nothing yet, and one that
	// asks for the document and never reads it.
	idle := holdGopherConns(t, addr, held-1)
	stalled := gopherDial(t, addr)
	defer func() { _ = stalled.Close() }()
	if _, err := io.WriteString(stalled, "/gophertest/"+p.Slug.String+"\r\n"); err != nil {
		t.Fatalf("send selector: %v", err)
	}

	// Once the stalled exchange runs out of time, its place is free again.
	if got := gopherFetch(t, addr, ""); !strings.HasSuffix(got, ".\r\n") {
		t.Fatalf("after the stalled exchange's deadline, the root menu is %q", got)
	}

	// The server closed the stalled connection before the whole answer.
	if err := stalled.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	n, err := io.Copy(io.Discard, stalled)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("stalled connection still open after reading %d bytes: %v", n, err)
	}
	if n >= int64(len(doc)) {
		t.Errorf("stalled client got the whole answer (%d bytes)", n)
	}

	// Finish the held requests, so that no server work outlives the test.
	for _, c := range idle {
		gopherRequest(t, c, "")
	}
}

func TestGopherHidesSilencedBlog(t *testing.T) {
	t.Parallel()

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
	})
	createGopherTestBlog(t, app, "openblog", CollPublic, "Open post", "Anyone may read this.")
	slug := createGopherTestBlog(t, app, "mutedblog", CollPublic, "Muted post", "Its owner is silenced.").Slug.String
	addr := startTestGopher(t, app)

	missing := gopherFetch(t, addr, "/nosuchblog/")
	if !strings.HasPrefix(missing, "3") {
		t.Fatalf("a missing blog answers %q, not an error item", missing)
	}
	for _, sel := range []string{"/mutedblog/", "/mutedblog/" + slug} {
		if got := gopherFetch(t, addr, sel); !strings.Contains(got, "Muted post") {
			t.Fatalf("%s is not served before the owner is silenced:\n%s", sel, got)
		}
	}

	_, err := app.db.ExecContext(t.Context(), "UPDATE users SET status = ? WHERE username = ?", UserSilenced, "mutedblog")
	if err != nil {
		t.Fatalf("silence the owner: %v", err)
	}

	root := gopherFetch(t, addr, "")
	if !strings.Contains(root, "/openblog/") {
		t.Errorf("root menu does not list the public blog:\n%s", root)
	}
	if strings.Contains(root, "mutedblog") {
		t.Errorf("root menu lists the silenced owner's blog:\n%s", root)
	}
	for _, sel := range []string{"/mutedblog/", "/mutedblog/" + slug} {
		got := gopherFetch(t, addr, sel)
		if strings.Contains(got, "Muted post") || strings.Contains(got, "Its owner is silenced.") {
			t.Errorf("%s serves the silenced owner's blog:\n%s", sel, got)
		}
		if got != missing {
			t.Errorf("%s answers %q, not the error item a missing blog gets (%q)", sel, got, missing)
		}
	}
}

func TestGopherSilencedBlogSingleUser(t *testing.T) {
	t.Parallel()

	app := newGopherTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = true
	})
	slug := createGopherTestBlog(t, app, "gophertest", CollPublic, "Hello", "A public post.").Slug.String
	addr := startTestGopher(t, app)

	_, err := app.db.ExecContext(t.Context(), "UPDATE users SET status = ?", UserSilenced)
	if err != nil {
		t.Fatalf("silence the owner: %v", err)
	}
	for _, sel := range []string{"", "/" + slug} {
		got := gopherFetch(t, addr, sel)
		if !strings.HasPrefix(got, "3") || strings.Contains(got, "Hello") {
			t.Errorf("%q answers %q, not an error item", sel, got)
		}
	}
}

func TestGopherServesNothingWhilePrivate(t *testing.T) {
	t.Parallel()

	for _, single := range []bool{false, true} {
		t.Run(strconv.FormatBool(single), func(t *testing.T) {
			t.Parallel()

			app := newGopherTestApp(t, func(cfg *config.Config) {
				cfg.App.SingleUser = single
			})
			slug := createGopherTestBlog(t, app, "openblog", CollPublic, "Open post", "Anyone may read this.").Slug.String
			addr := startTestGopher(t, app)

			selectors := []string{"", "/openblog/", "/openblog/" + slug, "/nosuchblog/"}
			if single {
				selectors = []string{"", "/" + slug, "/nosuchpost"}
			}
			served := make(map[string]string)
			for _, sel := range selectors {
				served[sel] = gopherFetch(t, addr, sel)
			}

			// The admin panel replaces the running config by assignment
			private := *app.Config()
			private.App.Private = true
			app.SetConfig(&private)

			wantErr := ""
			for _, sel := range selectors {
				got := gopherFetch(t, addr, sel)
				if !strings.HasPrefix(got, "3") || strings.Contains(got, "Open post") || strings.Contains(got, "Anyone may read this.") {
					t.Errorf("%q answers %q while private, not an error item", sel, got)
				}
				if wantErr == "" {
					wantErr = got
				} else if got != wantErr {
					t.Errorf("%q answers %q, not the same item as the others (%q)", sel, got, wantErr)
				}
			}

			public := private
			public.App.Private = false
			app.SetConfig(&public)
			for _, sel := range selectors {
				if got := gopherFetch(t, addr, sel); got != served[sel] {
					t.Errorf("%q answers %q after Private is off again, want %q", sel, got, served[sel])
				}
			}
		})
	}
}
