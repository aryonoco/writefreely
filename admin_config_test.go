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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/writefreely/writefreely/config"
)

// saveStubApper is an Apper whose SaveConfig outcome is set by the test.
type saveStubApper struct {
	app     *App
	saveErr error
	saved   *config.Config
}

func (s *saveStubApper) App() *App                                       { return s.app }
func (s *saveStubApper) LoadConfig() error                               { return nil }
func (s *saveStubApper) LoadKeys() error                                 { return nil }
func (s *saveStubApper) ReqLog(*http.Request, int, time.Duration) string { return "" }
func (s *saveStubApper) SaveConfig(c *config.Config) error {
	s.saved = c
	return s.saveErr
}

func postSettings(t *testing.T, s *saveStubApper, siteName string) error {
	t.Helper()
	form := url.Values{"site_name": {siteName}}
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/admin/update/config", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	return handleAdminUpdateConfig(s, &User{Username: "admin"}, w, r)
}

func TestAdminSaveFailureChangesNothing(t *testing.T) {
	t.Parallel()
	cfg := config.New()
	cfg.App.SiteName = "Before"
	s := &saveStubApper{app: &App{cfg: cfg}, saveErr: errors.New("disk is read-only")}

	err := postSettings(t, s, "After")
	if got := cfg.App.SiteName; got != "Before" {
		t.Errorf("running SiteName = %q after failed save, want %q", got, "Before")
	}
	if s.app.cfg.App.SiteName != "Before" {
		t.Errorf("app.cfg changed after failed save")
	}
	if err == nil || !strings.Contains(err.Error(), "disk+is+read-only") {
		t.Errorf("response does not carry the save error: %v", err)
	}
}

func TestAdminSaveSuccessApplies(t *testing.T) {
	t.Parallel()
	cfg := config.New()
	cfg.App.SiteName = "Before"
	s := &saveStubApper{app: &App{cfg: cfg}}

	err := postSettings(t, s, "After")
	if got := s.app.cfg.App.SiteName; got != "After" {
		t.Errorf("running SiteName = %q after save, want %q", got, "After")
	}
	if s.saved == nil || s.saved.App.SiteName != "After" {
		t.Errorf("SaveConfig was not given the new settings")
	}
	if err == nil || !strings.Contains(err.Error(), "Configuration+saved") {
		t.Errorf("response does not report success: %v", err)
	}
}

func TestAdminFormReadOnly(t *testing.T) { //nolint:paralleltest // renders through the package's global templates
	app, _ := newTemplateTestApp(t, nil)
	app.cfgFile = filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(app.cfgFile, []byte("[server]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	render := func() string {
		r := httptest.NewRequestWithContext(context.Background(), "GET", "/admin/settings", http.NoBody)
		w := httptest.NewRecorder()
		if err := handleViewAdminSettings(app, &User{Username: "admin"}, w, r); err != nil {
			t.Fatalf("settings page: %v", err)
		}
		return w.Body.String()
	}
	const notice = "config.ini can't be written"

	if body := render(); strings.Contains(body, notice) || strings.Contains(body, "<fieldset disabled") {
		t.Errorf("writable config.ini: page shows read-only notice or disabled fieldset")
	}

	if err := os.Chmod(app.cfgFile, 0o400); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(app.cfgFile, os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		t.Skip("file is writable despite mode 0444 (running as root?)")
	}
	body := render()
	if !strings.Contains(body, notice) {
		t.Errorf("read-only config.ini: notice missing from page")
	}
	if !strings.Contains(body, "<fieldset disabled") {
		t.Errorf("read-only config.ini: fieldset not disabled")
	}
}

func TestConfigWritableLeavesFileIntact(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{cfgFile: path}
	if !configWritable(app) {
		t.Fatal("configWritable = false for a writable file")
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" { //nolint:gosec // reading the temp file the test made
		t.Errorf("configWritable changed the file: %q", b)
	}
}
