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
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/writeas/web-core/activitystreams"
	"github.com/writeas/web-core/log"
)

const (
	// maxRemoteDocumentSize is the largest actor or WebFinger document read
	// from a remote server, as Mastodon limits the JSON it fetches.
	maxRemoteDocumentSize = 1 << 20

	// maxDeliveryResponseSize is how much of an inbox's answer to a
	// delivery is read. The answer is only logged, when debugging.
	maxDeliveryResponseSize = 4 << 10

	// keyRefetchInterval is how long the inbox waits, after fetching an
	// actor again because the key stored for it did not verify a request,
	// before doing so again for the same key ID. The stored key is still
	// tried meanwhile.
	keyRefetchInterval = 5 * time.Minute

	// keyRefetchCapacity is how many key IDs the inbox remembers refetches
	// of at once.
	keyRefetchCapacity = 10000
)

// errRemoteDocumentTooLarge means a remote document is larger than
// maxRemoteDocumentSize.
var errRemoteDocumentTooLarge = fmt.Errorf("remote document is larger than %d bytes", maxRemoteDocumentSize)

// readRemoteDocument reads a remote document from r, reading no more than
// one byte past maxRemoteDocumentSize, and refuses a larger one.
func readRemoteDocument(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxRemoteDocumentSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRemoteDocumentSize {
		return nil, errRemoteDocumentTooLarge
	}
	return b, nil
}

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

// keyRefetches remembers when each key ID's actor was last fetched again,
// so that it is fetched again at most once per keyRefetchInterval. It
// remembers at most capacity key IDs: those whose interval has passed are
// forgotten, and when it is full the oldest makes room. It is safe for
// concurrent use.
type keyRefetches struct {
	capacity int
	now      func() time.Time

	mu    sync.Mutex
	known map[string]struct{}
	// order holds the key IDs in known, oldest refetch first.
	order []keyRefetch
}

type keyRefetch struct {
	keyID string
	at    time.Time
}

// newKeyRefetches returns a keyRefetches that remembers at most capacity
// key IDs and reads the time from now.
func newKeyRefetches(capacity int, now func() time.Time) *keyRefetches {
	return &keyRefetches{
		capacity: capacity,
		now:      now,
		known:    make(map[string]struct{}),
	}
}

// allow reports whether keyID's actor may be fetched again now, and if so
// records that it is.
func (k *keyRefetches) allow(keyID string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.now()
	for len(k.order) > 0 && now.Sub(k.order[0].at) >= keyRefetchInterval {
		k.forgetOldest()
	}
	if _, ok := k.known[keyID]; ok {
		return false
	}
	if len(k.order) >= k.capacity {
		k.forgetOldest()
	}
	k.known[keyID] = struct{}{}
	k.order = append(k.order, keyRefetch{keyID: keyID, at: now})
	return true
}

func (k *keyRefetches) forgetOldest() {
	delete(k.known, k.order[0].keyID)
	k.order = k.order[1:]
}

// inboxKeyRefetches returns the App's record of key refetches, creating it
// on first use.
func (app *App) inboxKeyRefetches() *keyRefetches {
	app.keyRefetchOnce.Do(func() {
		app.keyRefetch = newKeyRefetches(keyRefetchCapacity, time.Now)
	})
	return app.keyRefetch
}
