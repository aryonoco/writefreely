/*
 * Copyright © 2020 Musing Studio LLC.
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
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/writeas/web-core/log"
	"github.com/writefreely/go-gopher"
	"github.com/writefreely/writefreely/config"
	"golang.org/x/net/netutil"
)

const (
	// gopherReadTimeout is how long a Gopher client has to send its selector.
	gopherReadTimeout = 10 * time.Second
	// gopherMaxConns is the most Gopher connections served at once.
	gopherMaxConns = 64
)

// gopherExchangeTimeout is how long a whole Gopher exchange may take, from
// accepting the connection to sending the end of the answer. Only tests
// change it.
var gopherExchangeTimeout = 120 * time.Second

// startGopher binds the Gopher port, then serves Gopher on it in the
// background. It returns an error if the port cannot be bound.
func startGopher(apper Apper) (net.Listener, error) {
	port := apper.App().Config().Server.GopherPort
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("listen on gopher port %d: %w", port, err)
	}
	l = &gopherListener{
		Listener:        netutil.LimitListener(l, gopherMaxConns),
		exchangeTimeout: gopherExchangeTimeout,
	}

	handler := NewWFHandler(apper)
	s := &gopher.Server{Handler: handler.Gopher(handleGopher)}
	log.Info("Serving on gopher://localhost:%d", port)
	go func() {
		log.Error("gopher: %v", s.Serve(l))
	}()
	return l, nil
}

// gopherListener gives each connection it accepts a deadline for the whole
// exchange and a shorter one for reading the selector, so that a client
// that sends nothing, or stops reading the answer, gives up its place.
type gopherListener struct {
	net.Listener
	exchangeTimeout time.Duration
}

func (l *gopherListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	// These fail only on a closed connection, whose next read fails too;
	// returning the error instead would stop the server.
	now := time.Now()
	_ = c.SetDeadline(now.Add(l.exchangeTimeout))
	_ = c.SetReadDeadline(now.Add(gopherReadTimeout))
	return &gopherConn{Conn: c}, nil
}

// gopherConn closes the connection as soon as a write to it fails, or runs
// past the exchange deadline, so that the client gives up its place under
// gopherMaxConns.
type gopherConn struct {
	net.Conn
}

func (c *gopherConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if err != nil {
		_ = c.Close()
	}
	return n, err
}

// gopherAdvertisedPort returns the port that Gopher links point to:
// gopher_advertised_port if it is set, or else gopher_port.
func gopherAdvertisedPort(cfg *config.Config) int {
	if cfg.Server.GopherAdvertisedPort > 0 {
		return cfg.Server.GopherAdvertisedPort
	}
	return cfg.Server.GopherPort
}

// gopherHidden reports whether c stays off Gopher: a private or
// password-protected blog is neither listed nor served there, nor is the
// blog of a silenced user.
func gopherHidden(app *App, c *Collection) (bool, error) {
	if c.IsPrivate() || c.IsProtected() {
		return true, nil
	}
	return app.db.IsUserSilenced(c.OwnerID)
}

// writeGopherText writes text as a Gopher text file (RFC 1436): every line
// ends in CRLF, a line starting with "." gets another "." in front, and a
// line holding a lone "." ends the file.
func writeGopherText(w io.Writer, text string) error {
	text = strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var b strings.Builder
	if text != "" {
		for line := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(line, ".") {
				b.WriteString(".")
			}
			b.WriteString(line)
			b.WriteString("\r\n")
		}
	}
	b.WriteString(".\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// Utility function to strip the URL from the hostname provided by app.cfg.App.Host
func stripHostProtocol(app *App) string {
	u, err := url.Parse(app.cfg.App.Host)
	if err != nil {
		// Fall back to host, with scheme stripped
		return string(regexp.MustCompile("^.*://").ReplaceAll([]byte(app.cfg.App.Host), []byte("")))
	}
	return u.Hostname()
}

func handleGopher(app *App, w gopher.ResponseWriter, r *gopher.Request) error {
	// The running config can be replaced while Gopher is up, so ask it
	// for every request
	if app.cfg.App.Private {
		return ErrCollectionNotFound
	}

	parts := strings.Split(r.Selector, "/")
	if app.cfg.App.SingleUser {
		if parts[1] != "" {
			return handleGopherCollectionPost(app, w, r)
		}
		return handleGopherCollection(app, w, r)
	}

	// Show all public collections (a gopher Reader view, essentially)
	if len(parts) == 3 {
		return handleGopherCollection(app, w, r)
	}

	w.WriteInfo(fmt.Sprintf("Welcome to %s", app.cfg.App.SiteName))

	colls, err := app.db.GetPublicCollections(app.cfg.App.Host)
	if err != nil {
		return err
	}

	for _, c := range *colls {
		if c.IsPrivate() || c.IsProtected() {
			continue
		}
		w.WriteItem(&gopher.Item{
			Host:        stripHostProtocol(app),
			Port:        gopherAdvertisedPort(app.cfg),
			Type:        gopher.DIRECTORY,
			Description: c.DisplayTitle(),
			Selector:    "/" + c.Alias + "/",
		})
	}
	return w.End()
}

func handleGopherCollection(app *App, w gopher.ResponseWriter, r *gopher.Request) error {
	var collAlias, slug string
	var c *Collection
	var err error
	var baseSel = "/"

	parts := strings.Split(r.Selector, "/")
	if app.cfg.App.SingleUser {
		// sanity check
		slug = parts[1]
		if slug != "" {
			return handleGopherCollectionPost(app, w, r)
		}

		c, err = app.db.GetCollectionByID(1)
		if err != nil {
			return err
		}
	} else {
		collAlias = parts[1]
		slug = parts[2]
		if slug != "" {
			return handleGopherCollectionPost(app, w, r)
		}

		c, err = app.db.GetCollection(collAlias)
		if err != nil {
			return err
		}
		baseSel = "/" + c.Alias + "/"
	}
	hidden, err := gopherHidden(app, c)
	if err != nil {
		return err
	}
	if hidden {
		return ErrCollectionNotFound
	}
	c.hostName = app.cfg.App.Host

	w.WriteInfo(c.DisplayTitle())
	if c.Description != "" {
		w.WriteInfo(c.Description)
	}

	posts, err := app.db.GetPosts(app.cfg, c, 0, false, false, false, "")
	if err != nil {
		return err
	}

	for _, p := range *posts {
		w.WriteItem(&gopher.Item{
			Port:        gopherAdvertisedPort(app.cfg),
			Host:        stripHostProtocol(app),
			Type:        gopher.FILE,
			Description: p.CreatedDate() + " - " + p.DisplayTitle(),
			Selector:    baseSel + p.Slug.String,
		})
	}
	return w.End()
}

func handleGopherCollectionPost(app *App, w gopher.ResponseWriter, r *gopher.Request) error {
	var collAlias, slug string
	var c *Collection
	var err error

	parts := strings.Split(r.Selector, "/")
	if app.cfg.App.SingleUser {
		slug = parts[1]
		c, err = app.db.GetCollectionByID(1)
		if err != nil {
			return err
		}
	} else {
		collAlias = parts[1]
		slug = parts[2]
		c, err = app.db.GetCollection(collAlias)
		if err != nil {
			return err
		}
	}
	hidden, err := gopherHidden(app, c)
	if err != nil {
		return err
	}
	if hidden {
		return ErrCollectionNotFound
	}
	c.hostName = app.cfg.App.Host

	p, err := app.db.GetPost(slug, c.ID)
	if err != nil {
		return err
	}

	b := bytes.Buffer{}
	if p.Title.String != "" {
		b.WriteString(p.Title.String + "\n")
	}
	b.WriteString(p.DisplayDate + "\n\n")
	b.WriteString(p.Content)
	if err := writeGopherText(w, b.String()); err != nil {
		return err
	}

	return w.End()
}
