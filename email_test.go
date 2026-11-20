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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// subscriptionRows counts the rows in emailsubscribers matching where.
func subscriptionRows(t *testing.T, app *App, where string, args ...any) int {
	t.Helper()
	var n int
	q := "SELECT COUNT(*) FROM emailsubscribers WHERE " + where
	if err := app.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	return n
}

// subscriptionTestApp is an instance with two users, "reader" and "other",
// each with a blog.
type subscriptionTestApp struct {
	app                   *App
	router                *mux.Router
	reader, other         *User
	readerColl, otherColl *Collection
}

func newSubscriptionTestApp(t *testing.T) subscriptionTestApp {
	t.Helper()
	app, router := newTemplateTestApp(t, nil)
	reader, readerColl, _ := createTemplateTestUser(t, app, "reader")
	other, otherColl, _ := createTemplateTestUser(t, app, "other")
	return subscriptionTestApp{app, router, reader, other, readerColl, otherColl}
}

// checkSubscriptionRecordedForRoute fails the test unless the subscription a
// request made went to the blog in the route and to no user or other blog.
func checkSubscriptionRecordedForRoute(t *testing.T, app *App, user *User, routeColl, otherColl *Collection) {
	t.Helper()
	if n := subscriptionRows(t, app, "user_id = ?", user.ID); n != 0 {
		t.Errorf("%d subscriptions recorded for user %d, want 0", n, user.ID)
	}
	if n := subscriptionRows(t, app, "collection_id = ?", otherColl.ID); n != 0 {
		t.Errorf("%d subscriptions recorded for blog %d, want 0", n, otherColl.ID)
	}
	if n := subscriptionRows(t, app, "collection_id = ? AND user_id IS NULL AND email = ?", routeColl.ID, "visitor@example.com"); n != 1 {
		t.Errorf("%d anonymous subscriptions recorded for blog %d, want 1", n, routeColl.ID)
	}
}

func TestSubscribeIgnoresUserAndBlogInJSON(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	ta := newSubscriptionTestApp(t)
	app, router, other, readerColl, otherColl := ta.app, ta.router, ta.other, ta.readerColl, ta.otherColl

	body := `{"email":"visitor@example.com","userid":` + strconv.FormatInt(other.ID, 10) + `,"collalias":"` + otherColl.Alias + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/"+readerColl.Alias+"/email/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	serveCSRFTest(router, req)

	checkSubscriptionRecordedForRoute(t, app, other, readerColl, otherColl)
}

func TestSubscribeIgnoresUserAndBlogInForm(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	ta := newSubscriptionTestApp(t)
	app, router, other, readerColl, otherColl := ta.app, ta.router, ta.other, ta.readerColl, ta.otherColl

	form := url.Values{
		"email":     {"visitor@example.com"},
		"userid":    {strconv.FormatInt(other.ID, 10)},
		"UserID":    {strconv.FormatInt(other.ID, 10)},
		"collalias": {otherColl.Alias},
		"CollAlias": {otherColl.Alias},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/"+readerColl.Alias+"/email/subscribe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	serveCSRFTest(router, req)

	checkSubscriptionRecordedForRoute(t, app, other, readerColl, otherColl)
}

func TestSubscribeRecordsLoggedInUser(t *testing.T) { //nolint:paralleltest // newTemplateTestApp loads the package-level templates
	ta := newSubscriptionTestApp(t)
	app, router, reader, other, otherColl := ta.app, ta.router, ta.reader, ta.other, ta.otherColl
	session := loginCookie(t, app, reader)
	token, csrfCookie := fetchCSRFToken(t, router, "/me/settings", session)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections/"+otherColl.Alias+"/email/subscribe", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(session)
	req.AddCookie(csrfCookie)
	serveCSRFTest(router, req)

	if n := subscriptionRows(t, app, "collection_id = ? AND user_id = ?", otherColl.ID, reader.ID); n != 1 {
		t.Errorf("%d subscriptions recorded for the logged-in user, want 1", n)
	}
	if n := subscriptionRows(t, app, "user_id = ?", other.ID); n != 0 {
		t.Errorf("%d subscriptions recorded for another user, want 0", n)
	}
}
