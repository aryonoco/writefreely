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
)

// Limits on the size of JSON request bodies that need no login to send.
const (
	// formBodyLimit is for requests like logging in or signing up, which
	// hold a few short fields.
	formBodyLimit = 64 << 10
	// markdownBodyLimit is for Markdown previews, which hold a whole post.
	markdownBodyLimit = 10 << 20
)

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
