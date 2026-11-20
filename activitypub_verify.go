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
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/writeas/httpsig"
	"github.com/writeas/impart"
	"github.com/writeas/web-core/activitypub"
	"github.com/writeas/web-core/log"
)

// The ways verifyInboxRequest refuses a request. Its errors wrap one of
// these.
var (
	// errUnsigned means the request carries no Signature header.
	errUnsigned = errors.New("request is not signed")
	// errBadSignature means the signature is malformed, does not cover
	// what it must, does not verify, or its key can't be fetched.
	errBadSignature = errors.New("signature does not verify")
	// errSignerGone means the signing key's actor answered 404 or 410:
	// the account no longer exists.
	errSignerGone = errors.New("signing actor is gone")
)

// errWrongActor means a fetched actor is not the one asked for, or not
// the request's signer.
var errWrongActor = errors.New("actor is not the one asked for")

// errInboxUnverified is the answer to an inbox request whose signature does
// not verify, or whose signer is not its actor.
var errInboxUnverified = impart.HTTPError{Status: http.StatusUnauthorized, Message: "Request signature could not be verified."}

// inboxSignedHeaders are what an inbox request's signature must cover.
var inboxSignedHeaders = []string{"(request-target)", "host", "date", "digest"}

// inboxMaxClockSkew is how far an inbox request's Date may be from now.
const inboxMaxClockSkew = 12 * time.Hour

// signatureParamRE matches one name="value" parameter of a Signature header.
var signatureParamRE = regexp.MustCompile(`([a-zA-Z]+)\s*=\s*"([^"]*)"`)

// inboxSignature is a parsed Signature header.
type inboxSignature struct {
	keyID string
	// keyActor is keyID without its fragment: where the key is fetched.
	keyActor  string
	algorithm string
	headers   []string
	signature []byte
}

// inboxKey is a remote actor's public key and the actor that owns it.
type inboxKey struct {
	pub   *rsa.PublicKey
	pem   string
	owner string
}

// verifyInboxRequest verifies the HTTP signature (draft-cavage, rsa-sha256)
// on an inbox request whose body has already been read, and returns the IRI
// of the actor that owns the signing key. The signature must cover the
// request target, Host, Date and Digest; the Digest must match body, and
// the Date must be within inboxMaxClockSkew of now.
//
// The key comes from remoteuserkeys when it is stored there, else from the
// actor document at the key ID without its fragment. A stored key that
// fails is fetched again once, in case the actor has replaced it.
func verifyInboxRequest(app *App, r *http.Request, body []byte) (signer string, err error) {
	sig, err := parseInboxSignature(r)
	if err != nil {
		log.Info("Inbox request not verified: %v", err)
		return "", err
	}
	signer, err = sig.verify(app, r, body)
	if err != nil {
		log.Info("Inbox request from %s not verified: %v", sig.keyActor, err)
		return "", err
	}
	return signer, nil
}

// parseInboxSignature reads r's Signature header.
func parseInboxSignature(r *http.Request) (*inboxSignature, error) {
	header := r.Header.Get("Signature")
	if header == "" {
		return nil, errUnsigned
	}

	sig := &inboxSignature{}
	for _, m := range signatureParamRE.FindAllStringSubmatch(header, -1) {
		switch m[1] {
		case "keyId":
			sig.keyID = m[2]
		case "algorithm":
			sig.algorithm = m[2]
		case "headers":
			sig.headers = strings.Fields(strings.ToLower(m[2]))
		case "signature":
			b, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil {
				return nil, fmt.Errorf("%w: signature is not base64", errBadSignature)
			}
			sig.signature = b
		}
	}
	if sig.keyID == "" || len(sig.signature) == 0 {
		return nil, fmt.Errorf("%w: no keyId or signature", errBadSignature)
	}

	u, err := url.Parse(sig.keyID)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: keyId is not a URL", errBadSignature)
	}
	u.Fragment, u.RawFragment = "", ""
	sig.keyActor = u.String()
	return sig, nil
}

// verify checks the algorithm, the signed headers, the body's Digest and
// the Date, then the signature itself.
func (sig *inboxSignature) verify(app *App, r *http.Request, body []byte) (string, error) {
	hosts, ok := inboxSignedHosts(app.cfg.App.Host)
	if !ok {
		return "", fmt.Errorf("%w: [app] host has no host name", errBadSignature)
	}
	// hs2019 leaves the algorithm to the key, and the key is RSA
	if sig.algorithm != "rsa-sha256" && sig.algorithm != "hs2019" {
		return "", fmt.Errorf("%w: algorithm is not rsa-sha256", errBadSignature)
	}
	for _, h := range inboxSignedHeaders {
		if !slices.Contains(sig.headers, h) {
			return "", fmt.Errorf("%w: %s is not signed", errBadSignature, h)
		}
	}
	if !digestMatches(r.Header.Values("Digest"), body) {
		return "", fmt.Errorf("%w: digest does not match the body", errBadSignature)
	}
	date, err := http.ParseTime(r.Header.Get("Date"))
	if err != nil {
		return "", fmt.Errorf("%w: no valid Date", errBadSignature)
	}
	if time.Since(date).Abs() > inboxMaxClockSkew {
		return "", fmt.Errorf("%w: Date is more than %v away", errBadSignature, inboxMaxClockSkew)
	}
	signed := make([][]byte, 0, len(hosts))
	for _, host := range hosts {
		signed = append(signed, httpsig.BuildSignatureData(inboxSignedRequest(r, host), sig.headers))
	}

	stored, ok := storedInboxKey(r.Context(), app, sig.keyID)
	if ok && verifyInboxSignature(stored.pub, signed, sig.signature) == nil {
		return stored.owner, nil
	}

	key, err := fetchInboxKey(app, sig.keyID, sig.keyActor) //nolint:contextcheck // resolveIRI takes no context; its client's timeout bounds the fetch
	if err != nil {
		return "", err
	}
	if err := verifyInboxSignature(key.pub, signed, sig.signature); err != nil {
		return "", fmt.Errorf("%w: %w", errBadSignature, err)
	}
	storeInboxKey(r.Context(), app, sig.keyID, key)
	return key.owner, nil
}

// inboxHostsLogged holds each [app] host value already logged as having
// no host name, so that it is logged once.
var inboxHostsLogged sync.Map

// inboxSignedHosts returns the Host values an inbox request's signature
// may be made for, whatever Host it arrived with (a proxy in front may
// have rewritten it): the configured host, lowercased and without the
// scheme's default port, then, when it has another port, the bare host
// name, as servers that sign without a port send it. It reports false,
// and logs it once, when configured has no host name.
func inboxSignedHosts(configured string) ([]string, bool) {
	u, err := url.Parse(configured)
	if err != nil || u.Hostname() == "" {
		if _, logged := inboxHostsLogged.LoadOrStore(configured, true); !logged {
			log.Error("Inbox requests can't be verified: [app] host %q has no host name", configured)
		}
		return nil, false
	}
	name := strings.ToLower(u.Hostname())
	bare := name
	if strings.Contains(name, ":") {
		bare = "[" + name + "]"
	}
	switch port := u.Port(); {
	case port == "",
		strings.EqualFold(u.Scheme, "http") && port == "80",
		strings.EqualFold(u.Scheme, "https") && port == "443":
		return []string{bare}, true
	default:
		return []string{net.JoinHostPort(name, port), bare}, true
	}
}

// inboxSignedRequest returns a copy of r with Host set to host.
func inboxSignedRequest(r *http.Request, host string) *http.Request {
	signed := r.Clone(r.Context())
	signed.Host = host
	return signed
}

// verifyInboxSignature checks signature with pub against each signing
// string in turn, and returns nil at the first it matches.
func verifyInboxSignature(pub *rsa.PublicKey, signed [][]byte, signature []byte) error {
	var err error
	for _, data := range signed {
		if err = httpsig.RSASHA256.Verify(pub, data, signature); err == nil {
			return nil
		}
	}
	return err
}

// digestMatches reports whether the Digest header values list a SHA-256
// digest, and every SHA-256 digest they list is body's.
func digestMatches(values []string, body []byte) bool {
	sum := sha256.Sum256(body)
	found := false
	for _, v := range values {
		for d := range strings.SplitSeq(v, ",") {
			algo, val, ok := strings.Cut(strings.TrimSpace(d), "=")
			if !ok || !strings.EqualFold(algo, "SHA-256") {
				continue
			}
			got, err := base64.StdEncoding.DecodeString(val)
			if err != nil || subtle.ConstantTimeCompare(got, sum[:]) != 1 {
				return false
			}
			found = true
		}
	}
	return found
}

// storedInboxKey returns the key stored under keyID, and the remote user it
// was stored for.
func storedInboxKey(ctx context.Context, app *App, keyID string) (inboxKey, bool) {
	var key inboxKey
	var pemKey []byte
	err := app.db.QueryRowContext(ctx, "SELECT k.public_key, u.actor_id FROM remoteuserkeys k INNER JOIN remoteusers u ON u.id = k.remote_user_id WHERE k.id = ?", keyID).Scan(&pemKey, &key.owner)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Error("Couldn't get remote user key %s: %v", keyID, err)
		}
		return key, false
	}
	key.pub, err = decodeRSAPublicKey(pemKey)
	if err != nil {
		return key, false
	}
	key.pem = string(pemKey)
	return key, true
}

// fetchInboxKey fetches the actor document at keyActor and returns the key
// it lists under keyID, with that key's owner. The owner must be on the
// key's host.
func fetchInboxKey(app *App, keyID, keyActor string) (inboxKey, error) {
	actor, err := fetchRemoteActor(app, keyActor)
	if err != nil {
		var status *remoteStatusError
		if errors.As(err, &status) && (status.StatusCode == http.StatusNotFound || status.StatusCode == http.StatusGone) {
			return inboxKey{}, errSignerGone
		}
		return inboxKey{}, fmt.Errorf("%w: key not fetched: %w", errBadSignature, err)
	}
	if actor.PublicKey.ID != keyID {
		return inboxKey{}, fmt.Errorf("%w: key not listed by %s", errBadSignature, keyActor)
	}
	owner := actor.PublicKey.Owner
	if owner == "" {
		owner = actor.ID
	}
	if !sameOrigin(owner, keyID) {
		return inboxKey{}, fmt.Errorf("%w: key owner is on another host", errBadSignature)
	}
	pub, err := decodeRSAPublicKey([]byte(actor.PublicKey.PublicKeyPEM))
	if err != nil {
		return inboxKey{}, fmt.Errorf("%w: %w", errBadSignature, err)
	}
	return inboxKey{pub: pub, pem: actor.PublicKey.PublicKeyPEM, owner: owner}, nil
}

// storeInboxKey replaces the key stored for the remote user key.owner, if
// there is one, with key, so that the next request finds it.
func storeInboxKey(ctx context.Context, app *App, keyID string, key inboxKey) {
	_, err := app.db.ExecContext(ctx, "UPDATE remoteuserkeys SET id = ?, public_key = ? WHERE remote_user_id = (SELECT id FROM remoteusers WHERE actor_id = ?)", keyID, key.pem, key.owner)
	if err != nil {
		log.Error("Couldn't update remote user key %s: %v", keyID, err)
	}
}

// decodeRSAPublicKey decodes a PEM-encoded RSA public key.
func decodeRSAPublicKey(pemKey []byte) (*rsa.PublicKey, error) {
	k, err := activitypub.DecodePublicKey(pemKey)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("key is not an RSA key")
	}
	return pub, nil
}

// sameOrigin reports whether URLs a and b are on the same host, as
// urlOrigin compares them.
func sameOrigin(a, b string) bool {
	oa, ok := urlOrigin(a)
	if !ok {
		return false
	}
	ob, ok := urlOrigin(b)
	return ok && oa == ob
}

// inboxActivityType returns the type of the activity in body, or "" when
// it has no string type.
func inboxActivityType(body []byte) string {
	var a struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &a) != nil {
		return ""
	}
	return a.Type
}

// activityActorIs reports whether signer is the actor of activity m and,
// when m is an Undo of an embedded activity, the actor of that one too.
func activityActorIs(m map[string]any, signer string) bool {
	if objectIRI(m["actor"]) != signer {
		return false
	}
	if m["type"] == "Undo" {
		if obj, ok := m["object"].(map[string]any); ok {
			if actor, ok := obj["actor"]; ok && objectIRI(actor) != signer {
				return false
			}
		}
	}
	return true
}

// objectIRI returns the IRI an ActivityStreams value names: the value itself
// when it is a string, or its id when it is an object.
func objectIRI(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	default:
		return ""
	}
}
