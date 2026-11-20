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
	"strings"

	"github.com/gofrs/uuid"
)

// parseAccessToken returns the access token in header, given as
// "Token <uuid>" or a bare uuid, as the forms auth.GetToken reads, or nil
// for any other value. Unlike GetToken, it logs nothing: the value may be
// a secret of another kind, like a password reset token in a query string.
func parseAccessToken(header string) []byte {
	if f := strings.Fields(header); len(f) == 2 && f[0] == "Token" {
		header = f[1]
	}
	t, err := uuid.FromString(header)
	if err != nil {
		return nil
	}
	return t[:]
}

// AuthenticateUser ensures a user with the given accessToken is valid. Call
// it before any operations that require authentication or optionally associate
// data with a user account.
// Returns an error if the given accessToken is invalid. Otherwise the
// associated user ID is returned.
func AuthenticateUser(db writestore, accessToken string) (int64, error) {
	if accessToken == "" {
		return 0, ErrNoAccessToken
	}
	userID := db.GetUserID(accessToken)
	if userID == -1 {
		return 0, ErrBadAccessToken
	}

	return userID, nil
}
