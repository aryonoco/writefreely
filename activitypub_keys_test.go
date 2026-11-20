//go:build sqlite

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
	"context"
	"crypto/rsa"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/writeas/web-core/activitypub"
)

func TestGenerateActorKeysParse(t *testing.T) {
	t.Parallel()
	pub, priv, err := generateActorKeys()
	require.NoError(t, err)

	pb, _ := pem.Decode(priv)
	require.NotNil(t, pb)
	assert.Equal(t, "PRIVATE KEY", pb.Type)
	pk, _ := pem.Decode(pub)
	require.NotNil(t, pk)
	assert.Equal(t, "PUBLIC KEY", pk.Type)

	privKey, err := activitypub.DecodePrivateKey(priv)
	require.NoError(t, err)
	rsaPriv, ok := privKey.(*rsa.PrivateKey)
	require.True(t, ok)
	assert.Equal(t, 2048, rsaPriv.N.BitLen())

	pubKey, err := activitypub.DecodePublicKey(pub)
	require.NoError(t, err)
	rsaPub, ok := pubKey.(*rsa.PublicKey)
	require.True(t, ok)
	assert.True(t, rsaPriv.PublicKey.Equal(rsaPub))
}

func TestGetAPActorKeysRepairsEmptyRow(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)
	_, err := app.db.ExecContext(context.Background(), "INSERT INTO collectionkeys (collection_id, public_key, private_key) VALUES (1, '', '')")
	require.NoError(t, err)

	pub, priv := app.db.GetAPActorKeys(1)
	assert.True(t, actorKeysUsable(pub, priv))
	_, err = activitypub.DecodePublicKey(pub)
	assert.NoError(t, err)
	_, err = activitypub.DecodePrivateKey(priv)
	assert.NoError(t, err)

	var dbPub, dbPriv []byte
	require.NoError(t, app.db.QueryRowContext(context.Background(), "SELECT public_key, private_key FROM collectionkeys WHERE collection_id = 1").Scan(&dbPub, &dbPriv))
	assert.Equal(t, pub, dbPub)
	assert.Equal(t, priv, dbPriv)
}

func TestGetAPActorKeysKeepsGoodRow(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)
	pub, priv, err := generateActorKeys()
	require.NoError(t, err)
	_, err = app.db.ExecContext(context.Background(), "INSERT INTO collectionkeys (collection_id, public_key, private_key) VALUES (1, ?, ?)", pub, priv)
	require.NoError(t, err)

	gotPub, gotPriv := app.db.GetAPActorKeys(1)
	assert.Equal(t, pub, gotPub)
	assert.Equal(t, priv, gotPriv)
}

func TestGetAPActorKeysCreatesMissingRow(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)
	pub, priv := app.db.GetAPActorKeys(7)
	assert.True(t, actorKeysUsable(pub, priv))
}

func TestGoSafeRecovers(t *testing.T) {
	t.Parallel()
	type recovered struct {
		name string
		v    interface{}
	}
	done := make(chan recovered, 1)
	// goSafeRecoveredHook is a test-only hook; see activitypub_keys.go.
	goSafeRecoveredHook = func(name string, v interface{}) { done <- recovered{name, v} }
	t.Cleanup(func() { goSafeRecoveredHook = nil })

	goSafe("t", func() { panic("boom") })

	select {
	case r := <-done:
		assert.Equal(t, "t", r.name)
		assert.Equal(t, "boom", r.v)
	case <-time.After(5 * time.Second):
		t.Fatal("goSafe did not recover")
	}
}
