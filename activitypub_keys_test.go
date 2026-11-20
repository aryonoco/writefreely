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
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/pem"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/writeas/web-core/activitypub"
	"github.com/writeas/web-core/activitystreams"
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

// getActorKeysAtOnce calls GetAPActorKeys for collectionID from n goroutines
// at once and returns the pair each one got.
func getActorKeysAtOnce(db *datastore, collectionID int64, n int) [][2][]byte {
	got := make([][2][]byte, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			pub, priv := db.GetAPActorKeys(collectionID)
			got[i] = [2][]byte{pub, priv}
		})
	}
	close(start)
	wg.Wait()
	return got
}

// assertOneStoredPair checks that collectionID has exactly one stored key
// pair, that it is usable, and that every caller got it.
func assertOneStoredPair(t *testing.T, db *datastore, collectionID int64, got [][2][]byte) {
	t.Helper()
	var rows int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM collectionkeys WHERE collection_id = ?", collectionID).Scan(&rows))
	require.Equal(t, 1, rows)

	var pub, priv []byte
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT public_key, private_key FROM collectionkeys WHERE collection_id = ?", collectionID).Scan(&pub, &priv))
	require.True(t, actorKeysUsable(pub, priv), "stored pair is not usable")

	for i, g := range got {
		assert.True(t, bytes.Equal(pub, g[0]) && bytes.Equal(priv, g[1]),
			"caller %d got a pair that isn't the stored one (%d and %d bytes)", i, len(g[0]), len(g[1]))
	}
}

func TestGetAPActorKeysConcurrentFirstUse(t *testing.T) {
	t.Parallel()
	app := newSignupTestApp(t)

	got := getActorKeysAtOnce(app.db, 7, 8)
	assertOneStoredPair(t, app.db, 7, got)
}

//nolint:paralleltest // captureLog swaps web-core's package-level loggers
func TestGetAPActorKeysConcurrentRepair(t *testing.T) {
	out := captureLog(t)
	app := newSignupTestApp(t)
	_, err := app.db.ExecContext(context.Background(), "INSERT INTO collectionkeys (collection_id, public_key, private_key) VALUES (1, '', '')")
	require.NoError(t, err)

	got := getActorKeysAtOnce(app.db, 1, 8)
	assertOneStoredPair(t, app.db, 1, got)

	logged := out.String()
	assert.Equal(t, 1, strings.Count(logged, "Replaced unusable activitypub keypair of collection 1\n"), "log:\n%s", logged)
	assert.NotContains(t, logged, "KEY-----")
}

//nolint:paralleltest // newFetchTestApp and allowTestServerDial set package-level variables
func TestResolveIRIWithoutKeyFails(t *testing.T) {
	app := newFetchTestApp(t)
	srv, hits := newActorServer(t)
	allowTestServerDial(t, srv)
	// With no table to read them from, the instance actor has no keys.
	_, err := app.db.ExecContext(context.Background(), "DROP TABLE collectionkeys")
	require.NoError(t, err)

	_, _, err = resolveIRI(app.cfg.App.Host, srv.URL+"/actor")
	require.Error(t, err)
	assert.Zero(t, hits.Load(), "the request was sent unsigned")
}

//nolint:paralleltest // allowTestServerDial sets a package-level variable
func TestMakeActivityPostWithoutKeyFails(t *testing.T) {
	srv, hits := newActorServer(t)
	allowTestServerDial(t, srv)
	p := activitystreams.NewPerson("https://writefreely.test/")

	err := makeActivityPost("https://writefreely.test", p, srv.URL+"/inbox", map[string]string{"type": "Follow"})
	require.Error(t, err)
	assert.Zero(t, hits.Load(), "the request was sent unsigned")
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
