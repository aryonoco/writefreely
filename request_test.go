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
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/writeas/web-core/auth"

	"github.com/writefreely/writefreely/config"
)

// The request body limits, written out rather than taken from the
// constants, so that a change to a constant shows here.
const (
	testFormBodyLimit    = 64 << 10
	testRequestBodyLimit = 10 << 20
)

// paddedJSON returns fields as a JSON object of exactly size bytes, filled
// out with a field no handler reads.
func paddedJSON(t *testing.T, fields map[string]any, size int) string {
	t.Helper()
	padded := map[string]any{"padding": ""}
	maps.Copy(padded, fields)
	b, err := json.Marshal(padded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) > size {
		t.Fatalf("%s is already longer than %d bytes", b, size)
	}
	padded["padding"] = strings.Repeat("a", size-len(b))
	if b, err = json.Marshal(padded); err != nil || len(b) != size {
		t.Fatalf("padded to %d bytes (%v), want %d", len(b), err, size)
	}
	return string(b)
}

// setTestBlogPassword protects the blog alias with password.
func setTestBlogPassword(t *testing.T, app *App, alias, password string) {
	t.Helper()
	hashed, err := auth.HashPass([]byte(password))
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := app.db.ExecContext(t.Context(), "INSERT INTO collectionpasswords (collection_id, password) VALUES ((SELECT id FROM collections WHERE alias = ?), ?)", alias, hashed); err != nil {
		t.Fatalf("set blog password: %v", err)
	}
}

// TestJSONBodyLimits checks that each JSON request that needs no login is
// read up to its limit and no further: a body of exactly the limit works,
// and one a byte longer gets 413 as a JSON error.
func TestJSONBodyLimits(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.App.OpenRegistration = true
	})
	createTemplateTestUser(t, app, "tester")
	setTestBlogPassword(t, app, "tester", "blogpassword")

	status := func(want int) func(*httptest.ResponseRecorder) bool {
		return func(rec *httptest.ResponseRecorder) bool { return rec.Code == want }
	}
	redirect := func(to string) func(*httptest.ResponseRecorder) bool {
		return func(rec *httptest.ResponseRecorder) bool {
			return rec.Code == http.StatusFound && rec.Header().Get("Location") == to
		}
	}
	const formLimit, markdownLimit = 64 << 10, 10 << 20
	for _, tc := range []struct { //nolint:paralleltest // the cases share one app and its database
		path   string
		limit  int
		fields map[string]any
		// worked reports whether rec shows the body was read and acted on.
		worked func(rec *httptest.ResponseRecorder) bool
	}{
		{"/api/markdown", markdownLimit, map[string]any{"raw_body": "*Hello*"}, status(http.StatusOK)},
		{"/api/auth/login", formLimit, map[string]any{"alias": "tester", "pass": "testpassword"}, status(http.StatusOK)},
		{"/api/auth/signup", formLimit, map[string]any{"alias": "newcomer", "pass": "newcomer-password"}, status(http.StatusCreated)},
		{"/api/alias", formLimit, map[string]any{"username": "available"}, status(http.StatusOK)},
		// With no address to subscribe, it goes back to the blog.
		{"/api/collections/tester/email/subscribe", formLimit, map[string]any{"email": ""}, redirect("http://localhost:8080/tester/")},
		{"/api/auth/read", formLimit, map[string]any{"alias": "tester", "password": "blogpassword"}, redirect("/tester/")},
		{"/auth/login", formLimit, map[string]any{"alias": "tester", "pass": "testpassword", "web": true}, status(http.StatusOK)},
		{"/auth/signup", formLimit, map[string]any{"alias": "webcomer", "pass": "webcomer-password"}, status(http.StatusCreated)},
	} {
		t.Run(strings.TrimPrefix(tc.path, "/"), func(t *testing.T) {
			post := func(body string) *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if strings.HasPrefix(tc.path, "/auth/") {
					// These forms need a CSRF token even with no session.
					token, cookie := fetchCSRFToken(t, router, "/login")
					req.Header.Set("X-CSRF-Token", token)
					req.AddCookie(cookie)
				}
				return serveCSRFTest(router, req)
			}

			// The longer body goes first, so that a signup it wrongly
			// completes makes the one within the limit fail too.
			rec := post(paddedJSON(t, tc.fields, tc.limit+1))
			var res struct {
				Code     int    `json:"code"`
				ErrorMsg string `json:"error_msg"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusRequestEntityTooLarge ||
				res.Code != http.StatusRequestEntityTooLarge || res.ErrorMsg == "" {
				t.Errorf("a body of %d bytes: %d %s, want 413 as a JSON error", tc.limit+1, rec.Code, truncateForLog(rec.Body.String()))
			}

			if rec := post(paddedJSON(t, tc.fields, tc.limit)); !tc.worked(rec) {
				t.Errorf("a body of %d bytes: %d %v %s, want it read and acted on", tc.limit, rec.Code, rec.Header(), truncateForLog(rec.Body.String()))
			}
		})
	}
}

// serveCounted serves req through h, and returns the response and how many
// bytes of req's body were read.
func serveCounted(h http.Handler, req *http.Request) (rec *httptest.ResponseRecorder, read int) {
	body := &countingReader{r: req.Body}
	req.Body = io.NopCloser(body)
	return serveCSRFTest(h, req), body.n
}

// formPart is one part of a multipart form: a field, or a file when
// filename is set.
type formPart struct {
	name, filename, value string
}

// newMultipartRequest returns a POST of parts, in order, to target.
func newMultipartRequest(t *testing.T, target string, parts ...formPart) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		var w io.Writer
		var err error
		if p.filename != "" {
			w, err = mw.CreateFormFile(p.name, p.filename)
		} else {
			w, err = mw.CreateFormField(p.name)
		}
		if err == nil {
			_, err = io.WriteString(w, p.value)
		}
		if err != nil {
			t.Fatalf("write form part %s: %v", p.name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// sessionCookieSet reports whether rec sets the session cookie.
func sessionCookieSet(rec *httptest.ResponseRecorder) bool {
	res := rec.Result()
	_ = res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			return true
		}
	}
	return false
}

// TestFormBodyLimitBeforeCSRFCheck checks that the login form's body is
// limited before the CSRF check reads it to find the token: a body far
// over the limit is read no further than the limit, and is refused even
// though it carries a valid token and the right password.
func TestFormBodyLimitBeforeCSRFCheck(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, _ := newCSRFTestApp(t, "http://localhost:8080")
	token, csrfCookie := fetchCSRFToken(t, router, "/login")

	req := newMultipartRequest(t, "/auth/login",
		formPart{name: "gorilla.csrf.Token", value: token},
		formPart{name: "alias", value: "tester"},
		formPart{name: "pass", value: "testpassword"},
		formPart{name: "padding", filename: "padding.txt", value: strings.Repeat("a", 1<<20)},
	)
	req.AddCookie(csrfCookie)
	rec, read := serveCounted(router, req)

	if read > testFormBodyLimit+1 {
		t.Errorf("read %d bytes of a 1 MiB login form, want at most %d", read, testFormBodyLimit+1)
	}
	// The CSRF check finds no token in a form it couldn't read whole.
	assertCSRFRefused(t, rec, "a 1 MiB login form")
	if sessionCookieSet(rec) {
		t.Error("a 1 MiB login form logged the visitor in")
	}
}

// TestFormBodyLimitRoutes checks that each route taking a few short fields
// from visitors who may have no session reads at most 64 KiB of a body. A
// session cookie, valid or not, has the CSRF check read the form, so every
// one of these routes reads the body.
func TestFormBodyLimitRoutes(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.App.OpenRegistration = true
		cfg.GenericOauth.ClientID = "test-client"
		cfg.GenericOauth.Host = "https://oauth.example"
	})
	createTemplateTestUser(t, app, "tester")

	for _, path := range []string{ //nolint:paralleltest // they share one app's templates
		"/auth/login",
		"/auth/signup",
		"/oauth/signup",
		"/reset",
		"/forgot-username",
		"/api/auth/login",
		"/api/auth/signup",
		"/api/auth/read",
		"/api/collections/tester/email/subscribe",
	} {
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			req := newMultipartRequest(t, path,
				formPart{name: "alias", value: "tester"},
				formPart{name: "padding", filename: "padding.txt", value: strings.Repeat("a", 1<<20)},
			)
			req.Header.Add("Cookie", cookieName+"=unknown")
			rec, read := serveCounted(router, req)

			if read > testFormBodyLimit+1 {
				t.Errorf("read %d bytes of a 1 MiB form, want at most %d", read, testFormBodyLimit+1)
			}
			assertCSRFRefused(t, rec, "a 1 MiB form")
		})
	}
}

// TestRequestBodyLimitWithSession checks that a form sent with a session to
// a route with no smaller limit, here a blog's settings, is read no further
// than 10 MiB, even by the CSRF check, and changes nothing.
func TestRequestBodyLimitWithSession(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, _, session := newCSRFTestApp(t, "http://localhost:8080")
	token, csrfCookie := fetchCSRFToken(t, router, "/new", session)

	const size = 12 << 20
	req := newMultipartRequest(t, "/api/collections/tester",
		formPart{name: "gorilla.csrf.Token", value: token},
		formPart{name: "title", value: "Changed by an oversized form"},
		formPart{name: "style_sheet", value: strings.Repeat("a", size)},
	)
	req.AddCookie(session)
	req.AddCookie(csrfCookie)
	rec, read := serveCounted(router, req)

	if read > testRequestBodyLimit+1 {
		t.Errorf("read %d bytes of a 12 MiB form, want at most %d", read, testRequestBodyLimit+1)
	}
	assertCSRFRefused(t, rec, "a 12 MiB form")
	var title string
	if err := app.db.QueryRowContext(t.Context(), "SELECT title FROM collections WHERE alias = ?", "tester").Scan(&title); err != nil {
		t.Fatalf("get blog title: %v", err)
	}
	if title == "Changed by an oversized form" {
		t.Error("a 12 MiB form changed the blog's title")
	}
}

// TestJSONBodyLimitBeforeCredentials checks that the JSON requests read
// before their credentials are checked, updating a post and creating a
// blog, answer a body a byte over 10 MiB with 413, with or without an
// access token, and change nothing.
func TestJSONBodyLimitBeforeCredentials(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.App.MaxBlogs = 5
	})
	u, _, post := createTemplateTestUser(t, app, "tester")
	accessToken, err := app.db.GetAccessToken(u.ID)
	if err != nil {
		t.Fatalf("get access token: %v", err)
	}

	for _, tc := range []struct { //nolint:paralleltest // the cases share one app and its database
		path   string
		fields map[string]any
		// changed reports whether the request was acted on.
		changed func(t *testing.T) bool
	}{
		{"/api/posts/" + post.ID, map[string]any{"body": "Changed by an oversized request."}, func(t *testing.T) bool {
			t.Helper()
			var content string
			if err := app.db.QueryRowContext(t.Context(), "SELECT content FROM posts WHERE id = ?", post.ID).Scan(&content); err != nil {
				t.Fatalf("get post: %v", err)
			}
			return content != post.Content
		}},
		{"/api/collections", map[string]any{"alias": "oversized", "title": "Oversized"}, func(t *testing.T) bool {
			t.Helper()
			var n int
			if err := app.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM collections WHERE alias = ?", "oversized").Scan(&n); err != nil {
				t.Fatalf("count blogs: %v", err)
			}
			return n != 0
		}},
	} {
		for _, withToken := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s token=%t", strings.TrimPrefix(tc.path, "/"), withToken), func(t *testing.T) {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path,
					strings.NewReader(paddedJSON(t, tc.fields, testRequestBodyLimit+1)))
				req.Header.Set("Content-Type", "application/json")
				if withToken {
					req.Header.Set("Authorization", accessToken)
				}
				rec := serveCSRFTest(router, req)

				var res struct {
					Code     int    `json:"code"`
					ErrorMsg string `json:"error_msg"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusRequestEntityTooLarge ||
					res.Code != http.StatusRequestEntityTooLarge || res.ErrorMsg == "" {
					t.Errorf("a body of %d bytes: %d %s, want 413 as a JSON error", testRequestBodyLimit+1, rec.Code, truncateForLog(rec.Body.String()))
				}
				if tc.changed(t) {
					t.Error("the oversized request was acted on")
				}
			})
		}
	}
}

// newImportRequest returns the import form, as the import page sends it,
// uploading files to the test user's blog.
func newImportRequest(t *testing.T, token string, files map[string]string) *http.Request {
	t.Helper()
	parts := make([]formPart, 0, 3+len(files))
	parts = append(parts,
		formPart{name: "gorilla.csrf.Token", value: token},
		formPart{name: "collection", value: "tester"},
		formPart{name: "fileDates", value: "{}"},
	)
	for name, content := range files {
		parts = append(parts, formPart{name: "files", filename: name, value: content})
	}
	return newMultipartRequest(t, "/api/me/import", parts...)
}

// TestBodyLimitsKeepNormalRequests checks that ordinary requests still
// work under the body limits: logging in with the form, updating a long
// post, and importing a post.
func TestBodyLimitsKeepNormalRequests(t *testing.T) {
	// The import handler saves uploads to temporary files.
	t.Setenv("TMPDIR", t.TempDir())
	app, router, u, session := newCSRFTestApp(t, "http://localhost:8080")
	accessToken, err := app.db.GetAccessToken(u.ID)
	if err != nil {
		t.Fatalf("get access token: %v", err)
	}
	var postID string
	if err = app.db.QueryRowContext(t.Context(), "SELECT id FROM posts WHERE owner_id = ?", u.ID).Scan(&postID); err != nil {
		t.Fatalf("get post: %v", err)
	}

	// Logging in with the form
	token, csrfCookie := fetchCSRFToken(t, router, "/login")
	form := url.Values{"alias": {"tester"}, "pass": {"testpassword"}, "gorilla.csrf.Token": {token}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := serveCSRFTest(router, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" || !sessionCookieSet(rec) {
		t.Errorf("POST /auth/login: %d to %q, want 302 to / with a session", rec.Code, rec.Header().Get("Location"))
	}

	// Updating a post longer than the limit on login forms
	content := strings.Repeat("A long post. ", 1<<20/13)
	body, err := json.Marshal(map[string]string{"body": content})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/posts/"+postID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", accessToken)
	rec = serveCSRFTest(router, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST /api/posts/%s: %d %s, want 200", postID, rec.Code, truncateForLog(rec.Body.String()))
	}
	var got string
	if err = app.db.QueryRowContext(t.Context(), "SELECT content FROM posts WHERE id = ?", postID).Scan(&got); err != nil {
		t.Fatalf("get post: %v", err)
	}
	if got != content {
		t.Errorf("the post holds %d bytes, want the %d sent", len(got), len(content))
	}

	// Importing a post
	token, csrfCookie = fetchCSRFToken(t, router, "/new", session)
	before := csrfTestPostCount(t, app)
	req = newImportRequest(t, token, map[string]string{"hello.md": "# Hello\n\nAn imported post."})
	req.AddCookie(session)
	req.AddCookie(csrfCookie)
	rec = serveCSRFTest(router, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/me/import" {
		t.Errorf("POST /api/me/import: %d to %q %s, want 302 to /me/import", rec.Code, rec.Header().Get("Location"), truncateForLog(rec.Body.String()))
	}
	if after := csrfTestPostCount(t, app); after != before+1 {
		t.Errorf("posts went from %d to %d, want one more", before, after)
	}
}

// TestImportBodyNotLimited checks that an import, which uploads many posts
// at once, isn't held to the limit on other requests: all of a 12 MiB
// import is read and imported.
func TestImportBodyNotLimited(t *testing.T) {
	// The import handler saves uploads to temporary files.
	t.Setenv("TMPDIR", t.TempDir())
	app, router, _, session := newCSRFTestApp(t, "http://localhost:8080")
	token, csrfCookie := fetchCSRFToken(t, router, "/new", session)
	before := csrfTestPostCount(t, app)

	const posts = 12
	files := make(map[string]string, posts)
	for i := range posts {
		files[fmt.Sprintf("post-%d.md", i)] = fmt.Sprintf("# Post %d\n\n", i) + strings.Repeat("A long post. ", 1<<20/13)
	}
	req := newImportRequest(t, token, files)
	req.AddCookie(session)
	req.AddCookie(csrfCookie)
	size := req.ContentLength
	rec, read := serveCounted(router, req)

	if read <= testRequestBodyLimit+1 {
		t.Errorf("read %d bytes of a %d-byte import, want more than %d", read, size, testRequestBodyLimit+1)
	}
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/me/import" {
		t.Errorf("POST /api/me/import: %d to %q %s, want 302 to /me/import", rec.Code, rec.Header().Get("Location"), truncateForLog(rec.Body.String()))
	}
	if after := csrfTestPostCount(t, app); after != before+posts {
		t.Errorf("posts went from %d to %d, want %d more", before, after, posts)
	}
}

// TestInboxBodyNotLimited checks that the ActivityPub inbox is held to its
// own limit, inbox_max_bytes, rather than the limit on other requests. Set
// to 12 MiB, it reads all of an 11 MiB delivery, whose missing signature
// it then refuses, and refuses a 13 MiB one as too large once it has read
// past 12 MiB.
func TestInboxBodyNotLimited(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	const inboxLimit = 12 << 20
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.Server.InboxMaxBytes = inboxLimit
	})
	createTemplateTestUser(t, app, "tester")

	for _, tc := range []struct { //nolint:paralleltest // the cases share one app's templates
		size, wantStatus int
		// wantRead reports whether reading n bytes of the body is right.
		wantRead func(n int) bool
	}{
		// Read to the end, then refused for its missing signature
		{11 << 20, http.StatusUnauthorized, func(n int) bool { return n == 11<<20 }},
		// Read past the limit on other requests, up to the inbox's own
		{13 << 20, http.StatusRequestEntityTooLarge, func(n int) bool { return n > testRequestBodyLimit+1 && n <= inboxLimit+1 }},
	} {
		t.Run(fmt.Sprintf("%d bytes", tc.size), func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/tester/inbox",
				strings.NewReader(paddedJSON(t, map[string]any{"type": "Create"}, tc.size)))
			req.Header.Set("Content-Type", "application/activity+json")
			rec, read := serveCounted(router, req)

			if !tc.wantRead(read) {
				t.Errorf("read %d bytes of a %d-byte delivery with a %d-byte inbox limit", read, tc.size, inboxLimit)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("a %d-byte delivery: %d %s, want %d", tc.size, rec.Code, truncateForLog(rec.Body.String()), tc.wantStatus)
			}
		})
	}
}
