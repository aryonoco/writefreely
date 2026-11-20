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
	"fmt"

	"github.com/writeas/web-core/activitystreams"
)

// fetchRemoteActor fetches the actor document at iri through the
// address-checked ActivityPub client, with the same dialer, timeout and
// signed request as every other remote fetch, and parses it.
func fetchRemoteActor(app *App, iri string) (*activitystreams.Person, error) {
	resp, err := resolveIRI(app.cfg.App.Host, iri)
	if err != nil {
		return nil, fmt.Errorf("fetch actor %s: %w", iri, err)
	}
	actor := &activitystreams.Person{}
	if err := unmarshalActor(resp, actor); err != nil {
		return nil, fmt.Errorf("parse actor %s: %w", iri, err)
	}
	return actor, nil
}

// sharedInboxOrInbox returns the actor's shared inbox, or its own inbox
// when it lists no shared one.
func sharedInboxOrInbox(actor *activitystreams.Person) string {
	if actor.Endpoints.SharedInbox != "" {
		return actor.Endpoints.SharedInbox
	}
	return actor.Inbox
}
