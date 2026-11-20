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
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/writeas/web-core/auth"

	"github.com/writefreely/writefreely/config"
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
