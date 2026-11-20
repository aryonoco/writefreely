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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"runtime/debug"

	"github.com/writeas/web-core/activitypub"
	"github.com/writeas/web-core/log"
)

// generateActorKeys creates a 2048-bit RSA key pair for an ActivityPub actor
// and returns it as PEM-encoded PKIX public and PKCS #8 private keys.
func generateActorKeys() (pubPEM, privPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}

	privPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return pubPEM, privPEM, nil
}

// actorKeysUsable reports whether both PEM-encoded keys decode.
func actorKeysUsable(pub, priv []byte) bool {
	// Check for a PEM block first, since the decoders expect one.
	if b, _ := pem.Decode(pub); b == nil {
		return false
	}
	if b, _ := pem.Decode(priv); b == nil {
		return false
	}
	if _, err := activitypub.DecodePublicKey(pub); err != nil {
		return false
	}
	if _, err := activitypub.DecodePrivateKey(priv); err != nil {
		return false
	}
	return true
}

// goSafeRecoveredHook, if set, is called after goSafe recovers from a panic.
// It is unexported and only meant for tests.
var goSafeRecoveredHook func(name string, v interface{})

// goSafe runs f in a new goroutine, logging and recovering from any panic so
// that it doesn't stop the whole process.
func goSafe(name string, f func()) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				log.Error("%s: recovered: %v\n%s", name, v, debug.Stack())
				if goSafeRecoveredHook != nil {
					goSafeRecoveredHook(name, v)
				}
			}
		}()
		f()
	}()
}
