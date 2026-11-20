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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/writefreely/writefreely/config"
)

// csrfCookieName is the cookie gorilla/csrf keeps a visitor's token in.
const csrfCookieName = "_gorilla_csrf"

var csrfMetaRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]*)"`)

// fetchCSRFToken GETs target from h with cookies, as a browser loads a page
// before it posts from it, and returns the CSRF token the page carries and
// the cookie that token belongs to.
func fetchCSRFToken(t *testing.T, h http.Handler, target string, cookies ...*http.Cookie) (token string, cookie *http.Cookie) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for _, c := range cookies {
		req.AddCookie(c)
		if c.Name == csrfCookieName {
			cookie = c
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	m := csrfMetaRe.FindStringSubmatch(rec.Body.String())
	if len(m) < 2 || m[1] == "" {
		t.Fatalf("GET %s: %d, the page carries no CSRF token", target, rec.Code)
	}
	res := rec.Result()
	_ = res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == csrfCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("GET %s: no %s cookie", target, csrfCookieName)
	}
	return html.UnescapeString(m[1]), cookie
}

// newCSRFTestApp returns a multi-user instance at host, served through the
// whole router, with one user, "tester", their blog and one post in it, and
// a session cookie for them.
func newCSRFTestApp(t *testing.T, host string) (app *App, router *mux.Router, u *User, session *http.Cookie) {
	t.Helper()
	app, router = newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.Host = host
		cfg.App.SingleUser = false
	})
	u, _, _ = createTemplateTestUser(t, app, "tester")
	return app, router, u, loginCookie(t, app, u)
}

func serveCSRFTest(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func csrfTestPostCount(t *testing.T, app *App) int {
	t.Helper()
	var n int
	if err := app.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM posts").Scan(&n); err != nil {
		t.Fatalf("count posts: %v", err)
	}
	return n
}

// newCSRFTestPost returns a JSON request that publishes a post to the test
// user's blog.
func newCSRFTestPost(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target,
		strings.NewReader(`{"body":"A post sent with a session cookie."}`))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCSRFRefusesCrossSiteWrite(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, _, session := newCSRFTestApp(t, "http://localhost:8080")
	before := csrfTestPostCount(t, app)

	req := newCSRFTestPost(t, "/api/collections/tester/posts")
	req.AddCookie(session)
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a write with the session cookie and no token: %d %s, want 403", rec.Code, rec.Body)
	}
	if after := csrfTestPostCount(t, app); after != before {
		t.Errorf("posts went from %d to %d", before, after)
	}
}

func TestCSRFRefusesAcceptJSONWithoutToken(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, session := newCSRFTestApp(t, "http://localhost:8080")

	form := url.Values{"email": {"tester@example.com"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/me/self", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(session)
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a form write that accepts JSON, with the session cookie and no token: %d %s, want 403", rec.Code, rec.Body)
	}
}

func TestCSRFAllowsTokenWrite(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, _, session := newCSRFTestApp(t, "http://localhost:8080")
	before := csrfTestPostCount(t, app)
	token, csrfCookie := fetchCSRFToken(t, router, "/new", session)

	req := newCSRFTestPost(t, "/api/collections/tester/posts")
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(session)
	req.AddCookie(csrfCookie)
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("a write with the editor's token: %d %s, want 201", rec.Code, rec.Body)
	}
	if after := csrfTestPostCount(t, app); after != before+1 {
		t.Errorf("posts went from %d to %d, want one more", before, after)
	}
}

func TestCSRFLoginFormHasToken(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, _ := newCSRFTestApp(t, "http://localhost:8080")

	page := serveCSRFTest(router, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", http.NoBody))
	if !strings.Contains(page.Body.String(), `name="gorilla.csrf.Token"`) {
		t.Errorf("GET /login: %d, the form has no gorilla.csrf.Token field", page.Code)
	}

	login := func(token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		form := url.Values{"alias": {"tester"}, "pass": {"testpassword"}}
		if token != "" {
			form.Set("gorilla.csrf.Token", token)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return serveCSRFTest(router, req)
	}

	if rec := login(""); rec.Code != http.StatusForbidden {
		t.Errorf("POST /auth/login with no session cookie and no token: %d %s, want 403", rec.Code, rec.Body)
	}
	token, csrfCookie := fetchCSRFToken(t, router, "/login")
	if rec := login(token, csrfCookie); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Errorf("POST /auth/login with the form's token: %d to %q, want 302 to /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCSRFSessionStartsNeedToken(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.App.OpenRegistration = true
		cfg.GenericOauth.ClientID = "test-client"
		cfg.GenericOauth.Host = "https://oauth.example"
	})
	createTemplateTestUser(t, app, "tester")

	// Each of these signs a visitor in, signs them up or acts on their
	// account without a session, so each needs the token from its form.
	for _, path := range []string{"/auth/signup", "/reset", "/forgot-username", "/oauth/signup", "/api/auth/login", "/api/auth/signup"} { //nolint:paralleltest // they share one app's templates
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			form := url.Values{"alias": {"tester"}, "pass": {"testpassword"}, "email": {"tester@example.com"}, "web": {"true"}}
			if strings.HasSuffix(path, "/signup") {
				form.Set("alias", "newcomer")
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			assertCSRFRefused(t, serveCSRFTest(router, req), "POST "+path+" with no session cookie and no token")
		})
	}

	// Another site's form can send JSON as text/plain and ask for a JSON
	// answer: only the Content-Type says whether a preflight was needed.
	t.Run("api/auth/login text/plain accepting JSON", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"alias":"tester","pass":"testpassword"}`))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Accept", "application/json")
		assertCSRFRefused(t, serveCSRFTest(router, req), "POST /api/auth/login as text/plain, accepting JSON, with no token")
	})
}

// assertCSRFRefused fails the test unless rec is the 403 that says to
// reload, which a handler's own 403 isn't.
func assertCSRFRefused(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "reload") {
		t.Errorf("%s: %d %s, want the 403 that says to reload", what, rec.Code, rec.Body)
	}
}

func TestCSRFTokenLastsAsLongAsSession(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, _ := newCSRFTestApp(t, "http://localhost:8080")

	_, cookie := fetchCSRFToken(t, router, "/login")
	if cookie.MaxAge != sessionLength {
		t.Errorf("the token cookie lasts %d s, want the session's %d s", cookie.MaxAge, sessionLength)
	}
}

func TestCSRFExemptsBearer(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, u, session := newCSRFTestApp(t, "http://localhost:8080")
	accessToken, err := app.db.GetAccessToken(u.ID)
	if err != nil {
		t.Fatalf("get access token: %v", err)
	}

	for _, header := range []string{"Token " + accessToken, accessToken} {
		for _, cookies := range [][]*http.Cookie{nil, {session}} {
			req := newCSRFTestPost(t, "/api/collections/tester/posts")
			req.Header.Set("Authorization", header)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rec := serveCSRFTest(router, req)

			if rec.Code != http.StatusCreated {
				t.Errorf("a write with an access token, %d cookies and no CSRF token: %d %s, want 201", len(cookies), rec.Code, rec.Body)
			}
		}
	}
}

func TestCSRFRefusesOtherAuthorization(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, u, session := newCSRFTestApp(t, "http://localhost:8080")
	email := func() string {
		var e string
		if err := app.db.QueryRowContext(t.Context(), "SELECT COALESCE(email, '') FROM users WHERE id = ?", u.ID).Scan(&e); err != nil {
			t.Fatalf("read email: %v", err)
		}
		return e
	}
	before := email()

	// Browsers send these on their own, to a proxy or a site that asked
	// for them before, so they say nothing about who made the request.
	for _, header := range []string{"Basic dXNlcjpwYXNz", "Negotiate YIIBhgYGKwYBBQUCoIIBejCCAXagMDAuBgkqhkiC9xIBAgIGCSqGSIb3EgECAgYKKwYBBAGCNwICHgYKKwYBBAGCNwICCqKCAUAEggE8", "Token not-an-access-token"} {
		form := url.Values{"email": {"other@example.net"}}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/me/self", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", header)
		req.AddCookie(session)
		rec := serveCSRFTest(router, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("a form write with Authorization %q, the session cookie and no token: %d %s, want 403", strings.Fields(header)[0], rec.Code, rec.Body)
		}
	}
	if after := email(); after != before {
		t.Errorf("the email went from %q to %q", before, after)
	}
}

func TestCSRFExemptsInbox(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, session := newCSRFTestApp(t, "http://localhost:8080")

	body := `{"@context":"https://www.w3.org/ns/activitystreams","type":"Follow","actor":"https://remote.example/users/a","object":"http://localhost:8080/api/collections/tester"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/tester/inbox", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/activity+json")
	req.AddCookie(session)
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned inbox POST with no CSRF token: %d %s, want the signature check's 401", rec.Code, rec.Body)
	}
}

func TestCSRFBehindTLSProxy(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router, _, session := newCSRFTestApp(t, "https://blog.example.com")

	// The proxy terminates TLS and passes requests on over plain HTTP, with
	// the visitor's Host or with its own.
	for _, tc := range []struct { //nolint:paralleltest // each case counts the posts it creates
		name, host, origin, referer string
		want                        int
	}{
		{"host kept", "blog.example.com", "https://blog.example.com", "https://blog.example.com/", http.StatusCreated},
		{"host rewritten", "127.0.0.1:8080", "https://blog.example.com", "https://blog.example.com/", http.StatusCreated},
		{"no origin, referer origin only", "127.0.0.1:8080", "", "https://blog.example.com/", http.StatusCreated},
		{"another origin", "blog.example.com", "https://elsewhere.example", "https://elsewhere.example/", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := csrfTestPostCount(t, app)
			token, csrfCookie := fetchCSRFToken(t, router, "http://"+tc.host+"/new", session)

			req := newCSRFTestPost(t, "http://"+tc.host+"/api/collections/tester/posts")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Header.Set("Referer", tc.referer)
			req.Header.Set("X-CSRF-Token", token)
			req.AddCookie(session)
			req.AddCookie(csrfCookie)
			rec := serveCSRFTest(router, req)

			if rec.Code != tc.want {
				t.Errorf("Host %s, Origin %q, Referer %q, with a token: %d %s, want %d",
					tc.host, tc.origin, tc.referer, rec.Code, rec.Body, tc.want)
			}
			wantPosts := before
			if tc.want == http.StatusCreated {
				wantPosts++
			}
			if after := csrfTestPostCount(t, app); after != wantPosts {
				t.Errorf("posts went from %d to %d, want %d", before, after, wantPosts)
			}
		})
	}
}

func TestCSRFRefusalSaysReload(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, session := newCSRFTestApp(t, "http://localhost:8080")
	// A tab rendered before its token changed sends a well-formed token
	// that matches no cookie the visitor still has.
	staleToken := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 64))

	// A browser's form gets a page.
	form := url.Values{"email": {"tester@example.com"}, "gorilla.csrf.Token": {staleToken}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/me/self", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.AddCookie(session)
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "reload") {
		t.Errorf("a stale form: %d %s, want 403 with a page that says to reload", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("a stale form: Content-Type %q, want text/html", ct)
	}

	// A script gets JSON.
	req = newCSRFTestPost(t, "/api/collections/tester/posts")
	req.Header.Set("X-CSRF-Token", staleToken)
	req.AddCookie(session)
	rec = serveCSRFTest(router, req)

	var res struct {
		Code     int    `json:"code"`
		ErrorMsg string `json:"error_msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("a stale request: %d %s, not JSON: %v", rec.Code, rec.Body, err)
	}
	if rec.Code != http.StatusForbidden || res.Code != http.StatusForbidden || !strings.Contains(res.ErrorMsg, "reload") {
		t.Errorf("a stale request: %d %s, want 403 with a message that says to reload", rec.Code, rec.Body)
	}
}

func TestCSRFAllowsAPILogin(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, _ := newCSRFTestApp(t, "http://localhost:8080")

	for _, contentType := range []string{"application/json", "application/json; charset=utf-8"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"alias":"tester","pass":"testpassword"}`))
		req.Header.Set("Content-Type", contentType)
		rec := serveCSRFTest(router, req)

		var res struct {
			Data struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusOK || res.Data.AccessToken == "" {
			t.Errorf("POST /api/auth/login as %s with no cookie or CSRF token: %d %s, want 200 with an access token", contentType, rec.Code, rec.Body)
		}
	}
}

func TestCSRFAllowsAPISignup(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.App.OpenRegistration = true
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/signup",
		strings.NewReader(`{"alias":"newcomer","pass":"newcomer-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("POST /api/auth/signup as JSON with no cookie or CSRF token: %d %s, want 201", rec.Code, rec.Body)
	}
	if _, err := app.db.GetUserForAuth("newcomer"); err != nil {
		t.Errorf("no account for newcomer: %v", err)
	}
}

func TestCSRFAllowsCookielessWrite(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	_, router, _, _ := newCSRFTestApp(t, "http://localhost:8080")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/markdown", strings.NewReader(`{"raw_body":"*Hello*"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := serveCSRFTest(router, req)

	if rec.Code != http.StatusOK {
		t.Errorf("POST /api/markdown with no cookie or CSRF token: %d %s, want 200", rec.Code, rec.Body)
	}
}

func TestCSRFEmailSubscribeShortcodeHasToken(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	app, router := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.SingleUser = false
		cfg.Email.Domain = "example.com"
		cfg.Email.MailgunPrivate = "unused"
	})
	u, coll, _ := createTemplateTestUser(t, app, "tester")
	if err := app.db.SetCollectionAttribute(coll.ID, "email_subs", "1"); err != nil {
		t.Fatalf("enable email subscriptions: %v", err)
	}
	title, content := "Subscribe", "Subscribe below.\n\n<!--emailsub-->"
	post, err := app.db.CreatePost(u.ID, coll.ID, &SubmittedPost{Title: &title, Content: &content})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	// A reader who has a session, but no subscription, gets the form.
	reader := &User{Username: "reader", HashedPass: []byte("unused")}
	if err := app.db.CreateUser(app.cfg, reader, "", ""); err != nil {
		t.Fatalf("create reader: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tester/"+post.Slug.String, http.NoBody)
	req.AddCookie(loginCookie(t, app, reader))
	rec := serveCSRFTest(router, req)

	form := regexp.MustCompile(`(?s)<form method="post" id="emailsub".*?</form>`).FindString(rec.Body.String())
	if form == "" {
		t.Fatalf("GET /tester/%s: %d, no subscribe form in the post", post.Slug.String, rec.Code)
	}
	if !strings.Contains(form, `name="gorilla.csrf.Token"`) {
		t.Errorf("the post's subscribe form has no CSRF token: %s", form)
	}
}

var (
	formTagRe    = regexp.MustCompile(`(?i)<form\b[^>]*>`)
	postMethodRe = regexp.MustCompile(`(?i)\bmethod\s*=\s*["']?post\b`)
	xhrOpenRe    = regexp.MustCompile(`\.open\(\s*["'](?:POST|PUT|PATCH|DELETE)["']`)
	headTagRe    = regexp.MustCompile(`(?i)<head\b`)
)

// csrfTokenProblems lists, by line, the places in src that would send a
// write without a CSRF token: forms that post without {{.CSRFField}},
// requests opened with an unsafe method and sent without an X-CSRF-Token
// header, and templates' <head>s with no csrf-token meta tag for scripts to
// read.
func csrfTokenProblems(path, src string) []string {
	var problems []string
	line := func(i int) int { return strings.Count(src[:i], "\n") + 1 }
	until := func(s, end string) string {
		if i := strings.Index(strings.ToLower(s), end); i >= 0 {
			return s[:i]
		}
		return s
	}

	for _, loc := range formTagRe.FindAllStringIndex(src, -1) {
		if postMethodRe.MatchString(src[loc[0]:loc[1]]) && !strings.Contains(until(src[loc[1]:], "</form>"), "CSRFField") {
			problems = append(problems, fmt.Sprintf("%s:%d: a form posts without {{.CSRFField}}", path, line(loc[0])))
		}
	}
	for _, loc := range xhrOpenRe.FindAllStringIndex(src, -1) {
		// The header must be set after open() and before send().
		if !strings.Contains(until(src[loc[1]:], ".send("), `setRequestHeader("X-CSRF-Token"`) {
			problems = append(problems, fmt.Sprintf("%s:%d: a request is sent without an X-CSRF-Token header", path, line(loc[0])))
		}
	}
	for _, loc := range headTagRe.FindAllStringIndex(src, -1) {
		if filepath.Ext(path) == ".tmpl" && !strings.Contains(until(src[loc[1]:], "</head>"), `<meta name="csrf-token" content="{{.CSRFToken}}">`) {
			problems = append(problems, fmt.Sprintf("%s:%d: a <head> has no csrf-token meta tag", path, line(loc[0])))
		}
	}
	return problems
}

func TestTemplatesSendToken(t *testing.T) {
	t.Parallel()
	var problems []string
	root := os.DirFS(".")
	for _, dir := range []string{templatesDir, pagesDir, "static/js"} {
		err := fs.WalkDir(root, dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || (filepath.Ext(path) != ".tmpl" && filepath.Ext(path) != ".js") {
				return err
			}
			src, err := fs.ReadFile(root, path)
			if err != nil {
				return err
			}
			problems = append(problems, csrfTokenProblems(path, string(src))...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(problems) > 0 {
		t.Errorf("%d places send a write without a CSRF token:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

func TestCSRFTokenProblemsFindsEach(t *testing.T) {
	t.Parallel()
	src := `<html><head><title>x</title></head>
<form method="post" action="/a"><input name="b"></form>
<form method="POST" action="/c">{{.CSRFField}}</form>
<form action="/d"></form>
<script>
http.open("POST", url, true);
http.send(params);
http.open("DELETE", url, true);
http.setRequestHeader("X-CSRF-Token", token);
http.send();
</script>`
	want := []string{
		"t.tmpl:2: a form posts without {{.CSRFField}}",
		"t.tmpl:6: a request is sent without an X-CSRF-Token header",
		"t.tmpl:1: a <head> has no csrf-token meta tag",
	}
	got := csrfTokenProblems("t.tmpl", src)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("csrfTokenProblems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
