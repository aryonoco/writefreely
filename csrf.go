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
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/writeas/impart"
	"github.com/writeas/web-core/log"

	"github.com/writefreely/writefreely/page"
)

// csrfRefusedMessage is what a write refused for its CSRF token is told.
// It's usually sent from a page rendered before the visitor's token
// changed, which a reload fixes.
const (
	csrfRefusedMessage = "This page has expired. Please reload it and try again."
	csrfRefusedHTML    = `<p style="text-align: center" class="introduction">` + csrfRefusedMessage + `</p>`
)

// csrfFormPaths are the forms that need a CSRF token even from a visitor
// with no session yet.
var csrfFormPaths = map[string]bool{
	"/auth/login":      true,
	"/auth/signup":     true,
	"/oauth/signup":    true,
	"/reset":           true,
	"/forgot-username": true,
}

// csrfAPIAuthPaths start a session too, so they need a CSRF token unless
// the body is JSON: another site can send that only after a preflight
// request, which is never answered.
var csrfAPIAuthPaths = map[string]bool{
	"/api/auth/login":  true,
	"/api/auth/signup": true,
}

// csrfInboxPath is the route template of the ActivityPub inbox.
const csrfInboxPath = "/api/collections/{alias}/inbox"

// csrfMiddleware requires a CSRF token on every unsafe request that carries
// the session cookie, and on the login, signup and account recovery forms
// and API calls, which start a session or act without one. Other requests
// pass without one: those authenticated by an access token in the
// Authorization header, which a browser never adds on its own, deliveries
// to the inbox, which verifies their signatures, and requests with no
// session cookie. Safe requests pass too, and get a token for their pages'
// forms and scripts to send back.
func csrfMiddleware(apper Apper) mux.MiddlewareFunc {
	app := apper.App()
	host := app.cfg.App.Host
	secure := strings.HasPrefix(host, "https://")
	var trusted []string
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		// Behind a proxy that terminates TLS, the Host a request arrives
		// with may not be the one the visitor's browser sent it to.
		trusted = append(trusted, u.Host)
	}
	protect := csrf.Protect(app.keys.CSRFKey,
		csrf.Path("/"),
		// A token lasts as long as the session it goes with, so a page
		// left open, like the editor, can still post.
		csrf.MaxAge(sessionLength),
		csrf.Secure(secure),
		csrf.TrustedOrigins(trusted),
		csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csrfRefused(app, w, r)
		})),
	)

	return func(next http.Handler) http.Handler {
		protected := protect(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !secure {
				r = csrf.PlaintextHTTPRequest(r)
			}
			if csrfExempt(r) {
				r = csrf.UnsafeSkipCheck(r)
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// csrfExempt reports whether r may change state without a CSRF token.
func csrfExempt(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		// These aren't checked, and they're where pages get their tokens.
		return false
	}
	if isAccessToken(r.Header.Get("Authorization")) {
		return true
	}
	if route := mux.CurrentRoute(r); route != nil {
		if tmpl, err := route.GetPathTemplate(); err == nil && tmpl == csrfInboxPath {
			return true
		}
	}
	if csrfFormPaths[r.URL.Path] || (csrfAPIAuthPaths[r.URL.Path] && !hasJSONBody(r)) {
		return false
	}
	_, err := r.Cookie(cookieName)
	return err != nil
}

// isAccessToken reports whether an Authorization header holds an access
// token. It logs nothing for the other schemes, like Basic, that browsers
// send.
func isAccessToken(header string) bool {
	return parseAccessToken(header) != nil
}

// hasJSONBody reports whether r says its body is JSON.
func hasJSONBody(r *http.Request) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && ct == "application/json"
}

// csrfRefused answers a request refused for its CSRF token with 403 and a
// message that says to reload: a page for a browser, JSON for a script.
func csrfRefused(app *App, w http.ResponseWriter, r *http.Request) {
	log.Info("Refused %s %s: %v", r.Method, r.URL.Path, csrf.FailureReason(r))

	t, ok := pages["blank.tmpl"]
	if !ok || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		_ = impart.WriteError(w, impart.HTTPError{Status: http.StatusForbidden, Message: csrfRefusedMessage})
		return
	}
	p := struct {
		page.StaticPage
		Title   string
		Content template.HTML
	}{
		StaticPage: pageForReq(app, r),
		Title:      "Page expired",
		Content:    csrfRefusedHTML,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	if err := t.ExecuteTemplate(w, "base", p); err != nil {
		log.Error("Unable to render the expired page: %v", err)
	}
}
