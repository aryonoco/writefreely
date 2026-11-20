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
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/writeas/web-core/activitystreams"
	"github.com/writeas/web-core/log"
)

// fetchRemoteActor fetches the actor document at iri through the
// address-checked ActivityPub client, with the same dialer, timeout and
// signed request as every other remote fetch, and parses it.
//
// A host speaks only for its own actors and keys, so the document's id,
// and its key's id and owner, must be on the host that served it: that of
// iri, or of the last redirect the fetch followed.
func fetchRemoteActor(app *App, iri string) (*activitystreams.Person, error) {
	resp, servedFrom, err := resolveIRI(app.cfg.App.Host, iri)
	if err != nil {
		return nil, fmt.Errorf("fetch actor %s: %w", iri, err)
	}
	actor := &activitystreams.Person{}
	if err := unmarshalActor(resp, actor); err != nil {
		return nil, fmt.Errorf("parse actor %s: %w", iri, err)
	}
	if err := checkActorOrigin(actor, servedFrom); err != nil {
		return nil, err
	}
	return actor, nil
}

// checkActorOrigin returns an error unless actor's id, and its key's id and
// owner when it lists a key, are on the same host as servedFrom.
func checkActorOrigin(actor *activitystreams.Person, servedFrom string) error {
	served, ok := urlOrigin(servedFrom)
	if !ok {
		return errors.New("actor document served from no host")
	}
	named := []string{actor.ID}
	for _, iri := range []string{actor.PublicKey.ID, actor.PublicKey.Owner} {
		if iri != "" {
			named = append(named, iri)
		}
	}
	for _, iri := range named {
		if origin, ok := urlOrigin(iri); !ok || origin != served {
			host := "no host"
			if ok {
				host = origin
			}
			log.Info("Refusing actor document from %s: it names %s", served, host)
			return fmt.Errorf("actor document from %s names %s", served, host)
		}
	}
	return nil
}

// urlOrigin returns the scheme, host and port of URL s, lowercased, with
// the scheme's default port when s gives none. Two URLs are on the same
// host when their origins are equal.
func urlOrigin(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return scheme + "://" + net.JoinHostPort(host, port), true
}

// sharedInboxOrInbox returns the actor's shared inbox, or its own inbox
// when it lists no shared one.
func sharedInboxOrInbox(actor *activitystreams.Person) string {
	if actor.Endpoints.SharedInbox != "" {
		return actor.Endpoints.SharedInbox
	}
	return actor.Inbox
}
