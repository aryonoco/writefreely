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
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/writeas/web-core/log"
)

const (
	// loginFailureLimit is how many logins a client address may fail within
	// loginFailureWindow before its attempts are refused.
	loginFailureLimit  = 10
	loginFailureWindow = 15 * time.Minute

	// loginLimiterCapacity is how many client addresses the login limiter
	// tracks at once.
	loginLimiterCapacity = 10000
)

// unknownUserHash is a bcrypt hash, at the cost auth.HashPass uses, of a
// random password that was then discarded. A login for a name that doesn't
// exist is checked against it, so it takes as long as one for a name that
// does.
const unknownUserHash = "$2a$12$IvbX3U9IQi0R63iZHdsYW.zDXl5G8qXgEAoxSeEBAywpUtqFlQ.Rm"

// loginLimiter counts login attempts per client address. allow counts an
// attempt in the same step that admits it, so attempts made at once can't
// overshoot the limit, and succeeded hands the attempt back when the
// password was right; what stays counted are failures. An address's window
// opens at its first counted attempt; once limit attempts are counted
// within it, allow refuses that address until the window closes. At most
// capacity addresses are tracked: when the table is full, expired entries
// are dropped, and failing that the entry whose window opened first.
type loginLimiter struct {
	limit    int
	window   time.Duration
	capacity int

	mu      sync.Mutex
	entries map[string]*addrAttempts
}

type addrAttempts struct {
	start time.Time
	count int
	// refused is set once an attempt has been refused in this window.
	refused bool
}

// newLoginLimiter returns a limiter that refuses an address after limit
// counted attempts within window, tracking at most capacity addresses.
//
//nolint:unparam // the limits stay parameters so the limiter type doesn't depend on the package's constants
func newLoginLimiter(limit int, window time.Duration, capacity int) *loginLimiter {
	return &loginLimiter{
		limit:    limit,
		window:   window,
		capacity: capacity,
		entries:  make(map[string]*addrAttempts),
	}
}

func (l *loginLimiter) expired(a *addrAttempts, now time.Time) bool {
	return !now.Before(a.start.Add(l.window))
}

// allow reports whether addr may attempt a login at now, and if so counts
// the attempt.
func (l *loginLimiter) allow(addr string, now time.Time) bool {
	allowed, _ := l.attempt(addr, now)
	return allowed
}

// attempt is allow, and also reports whether the attempt is the first one
// refused within addr's window, so the caller can log that the address
// reached the limit once.
func (l *loginLimiter) attempt(addr string, now time.Time) (allowed, first bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.entries[addr]
	if ok && !l.expired(a, now) {
		if a.count >= l.limit {
			first = !a.refused
			a.refused = true
			return false, first
		}
		a.count++
		return true, false
	}
	if !ok && len(l.entries) >= l.capacity {
		l.makeRoom(now)
	}
	l.entries[addr] = &addrAttempts{start: now, count: 1}
	return true, false
}

// succeeded hands back the attempt allow counted for addr, once that login
// has succeeded.
func (l *loginLimiter) succeeded(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.entries[addr]
	if !ok {
		return
	}
	a.count--
	if a.count <= 0 {
		delete(l.entries, addr)
	}
}

// makeRoom drops every expired entry, or, when none has expired, the entry
// whose window opened first.
func (l *loginLimiter) makeRoom(now time.Time) {
	var oldest string
	var oldestStart time.Time
	found := false
	for addr, a := range l.entries {
		if l.expired(a, now) {
			delete(l.entries, addr)
			continue
		}
		if !found || a.start.Before(oldestStart) {
			oldest, oldestStart, found = addr, a.start, true
		}
	}
	if len(l.entries) >= l.capacity && found {
		delete(l.entries, oldest)
	}
}

// loginAttempts returns the App's login limiter, creating it on first use.
func (app *App) loginAttempts() *loginLimiter {
	app.loginLimitOnce.Do(func() {
		app.loginLimit = newLoginLimiter(loginFailureLimit, loginFailureWindow, loginLimiterCapacity)
	})
	return app.loginLimit
}

// logLoginLimit logs that addr has reached the login limit. It names the
// address and the window, never the name typed at the login. When no proxy
// is listed it also says what to check if many visitors reach the limit.
func logLoginLimit(addr string, trusted proxyList) {
	msg := "Login attempt limit reached for %s: further attempts from it are refused for up to %s."
	if len(trusted.prefixes) == 0 && !trusted.unix {
		log.Error(msg+" [server] trusted_proxies is empty: behind a reverse proxy every visitor shares the proxy's address, so list the proxy in [server] trusted_proxies.", addr, loginFailureWindow)
		return
	}
	log.Error(msg, addr, loginFailureWindow)
}

// proxyList is a parsed [server] trusted_proxies list.
type proxyList struct {
	prefixes []netip.Prefix
	// unix is set by the entry unix, and lists a peer on a Unix socket.
	unix bool
}

func (pl proxyList) contains(a netip.Addr) bool {
	for _, p := range pl.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseTrustedProxies parses a comma-separated list of IP addresses, CIDR
// prefixes, and the word unix, which lists a peer on a Unix socket. Empty
// entries are skipped.
func parseTrustedProxies(s string) (proxyList, error) {
	var pl proxyList
	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
		case entry == "unix":
			pl.unix = true
		case strings.Contains(entry, "/"):
			p, err := netip.ParsePrefix(entry)
			if err != nil {
				return proxyList{}, fmt.Errorf("%q is not an IP address, a CIDR prefix or unix", entry)
			}
			pl.prefixes = append(pl.prefixes, p.Masked())
		default:
			a, err := netip.ParseAddr(entry)
			if err != nil {
				return proxyList{}, fmt.Errorf("%q is not an IP address, a CIDR prefix or unix", entry)
			}
			a = a.Unmap().WithZone("")
			pl.prefixes = append(pl.prefixes, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return pl, nil
}

// forwardedForWarned records that an X-Forwarded-For header from a peer
// outside [server] trusted_proxies has been logged, so it's logged once.
var forwardedForWarned atomic.Bool

// parseAddr parses an IP address, with or without a port.
func parseAddr(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

// clientAddr returns the address r's login attempts are counted against.
// That is the peer's address, unless the peer is listed in [server]
// trusted_proxies: then it is the right-most address in X-Forwarded-For
// that isn't listed. A peer on a Unix socket is listed only by the entry
// unix.
func clientAddr(r *http.Request, trusted proxyList) string {
	forwarded := r.Header.Values("X-Forwarded-For")
	addr := r.RemoteAddr
	peer := r.RemoteAddr
	listed := false
	entry := "the proxy"
	if sock, ok := r.Context().Value(http.LocalAddrContextKey).(*net.UnixAddr); ok {
		peer = "the Unix socket " + sock.String()
		entry = "the proxy (or unix)"
		listed = trusted.unix
	} else if a, ok := parseAddr(r.RemoteAddr); ok {
		addr = a.String()
		peer = addr
		listed = trusted.contains(a)
	}
	if !listed {
		if len(forwarded) > 0 && forwardedForWarned.CompareAndSwap(false, true) {
			log.Error("[WARNING] Ignoring X-Forwarded-For from %s, which isn't in [server] trusted_proxies. If WriteFreely runs behind a reverse proxy, list %s there. This is logged once.", peer, entry)
		}
		return addr
	}

	hops := strings.Split(strings.Join(forwarded, ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseAddr(strings.TrimSpace(hops[i]))
		if !ok {
			break
		}
		addr = hop.String()
		if !trusted.contains(hop) {
			break
		}
	}
	return addr
}
