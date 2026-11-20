/*
 * Copyright © 2018 Musing Studio LLC.
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
	"mime"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Limits on the size of request bodies.
const (
	// formBodyLimit is for requests like logging in or signing up, which
	// hold a few short fields.
	formBodyLimit = 64 << 10
	// requestBodyLimit is for every other request but an import or an
	// ActivityPub delivery. The largest of them, like a post or a Markdown
	// preview, hold one whole post. It's also the limit net/http puts on a
	// URL-encoded form.
	requestBodyLimit = 10 << 20
)

// formBodyRoutes are the routes, by method and path template, that take a
// few short fields from visitors who may have no session yet: logging in,
// signing up, recovering an account, unlocking a blog and subscribing to
// one. Their bodies get formBodyLimit. The CSRF check reads the login,
// signup and recovery forms even from a visitor with no session.
var formBodyRoutes = map[string]bool{
	"POST /auth/login":                              true,
	"POST /auth/signup":                             true,
	"POST /oauth/signup":                            true,
	"POST /reset":                                   true,
	"POST /forgot-username":                         true,
	"POST /api/auth/login":                          true,
	"POST /api/auth/signup":                         true,
	"POST /api/auth/read":                           true,
	"POST /api/collections/{alias}/email/subscribe": true,
}

// ownBodyRoutes are the routes, by method and path template, whose bodies
// are left to their handlers.
var ownBodyRoutes = map[string]bool{
	// An import uploads many post files in one request.
	"POST /api/me/import": true,
	// The ActivityPub inbox limits its body to the configured
	// inbox_max_bytes before it reads it, and the CSRF check, which
	// deliveries are exempt from, never reads it.
	"POST " + csrfInboxPath: true,
}

// limitRequestBodies bounds the body of every request before anything,
// the CSRF check included, reads it: when no header carries the CSRF
// token, the check parses a form body to look for it. A body over its
// limit fails to parse wherever it's read, so the CSRF check finds no
// token in it, and the server closes the connection after the response
// rather than read the rest.
func limitRequestBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limit := bodyLimit(r); limit > 0 && r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit returns the most bytes of r's body that its route reads, or 0
// for no limit. The route is the one the router matched r to, by method
// and path template, so no spelling of a path gets another route's limit.
func bodyLimit(r *http.Request) int64 {
	var route string
	if cur := mux.CurrentRoute(r); cur != nil {
		if tmpl, err := cur.GetPathTemplate(); err == nil {
			route = r.Method + " " + tmpl
		}
	}
	switch {
	case ownBodyRoutes[route]:
		return 0
	case formBodyRoutes[route]:
		return formBodyLimit
	default:
		return requestBodyLimit
	}
}

func IsJSON(r *http.Request) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	accept := r.Header.Get("Accept")
	return ct == "application/json" || accept == "application/json"
}

func IsActivityPubRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/activity+json") ||
		accept == "application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\""
}

// decodeJSONBody decodes the JSON body of r into v, reading at most limit
// bytes of it. A longer body gets ErrBodyTooLarge.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return ErrBodyTooLarge
	}
	return err
}
