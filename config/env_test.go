/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const smtpIni = "[app]\nhost = http://localhost:8080\n[email]\nsmtp_password = file\n"

func loadWith(t *testing.T, env map[string]string) *Config {
	t.Helper()
	cfg, err := Load(writeCfg(t, smtpIni))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ApplyEnv(envMap(env), os.ReadFile); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestApplyEnvOverridesSecret(t *testing.T) {
	t.Parallel()
	cfg := loadWith(t, map[string]string{"WRITEFREELY_SMTP_PASSWORD": "env"})
	if cfg.Email.Password != "env" {
		t.Fatalf("got %q, want env", cfg.Email.Password)
	}
}

func TestLoadAppliesEnv(t *testing.T) {
	t.Setenv("WRITEFREELY_SMTP_PASSWORD", "env")
	cfg, err := Load(writeCfg(t, smtpIni))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Email.Password != "env" {
		t.Fatalf("got %q, want env", cfg.Email.Password)
	}
}

func TestApplyEnvFile(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"fromfile\n":   "fromfile",
		"fromfile\r\n": "fromfile",
		"fromfile\n\n": "fromfile\n",
		"fromfile":     "fromfile",
		"fromfile\r":   "fromfile\r",
	} {
		p := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(p, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := loadWith(t, map[string]string{"WRITEFREELY_SMTP_PASSWORD_FILE": p})
		if cfg.Email.Password != want {
			t.Errorf("%q: got %q, want %q", in, cfg.Email.Password, want)
		}
	}
}

func TestApplyEnvRefusesBoth(t *testing.T) {
	t.Parallel()
	cfg := New()
	err := cfg.ApplyEnv(envMap(map[string]string{
		"WRITEFREELY_SMTP_PASSWORD":      "a",
		"WRITEFREELY_SMTP_PASSWORD_FILE": "/x",
	}), os.ReadFile)
	if err == nil || !strings.Contains(err.Error(), "WRITEFREELY_SMTP_PASSWORD") ||
		!strings.Contains(err.Error(), "WRITEFREELY_SMTP_PASSWORD_FILE") {
		t.Fatalf("got %v", err)
	}
}

func TestApplyEnvUnreadableFile(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	cfg := New()
	err := cfg.ApplyEnv(envMap(map[string]string{"WRITEFREELY_SMTP_PASSWORD_FILE": missing}), os.ReadFile)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, w := range []string{missing, "uid", strconv.Itoa(os.Getuid())} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q lacks %q", err, w)
		}
	}
}

func TestApplyEnvErrorOmitsContents(t *testing.T) {
	t.Parallel()
	cfg := New()
	err := cfg.ApplyEnv(envMap(map[string]string{"WRITEFREELY_SMTP_PASSWORD_FILE": "/p"}),
		func(string) ([]byte, error) { return []byte("hunter2"), os.ErrPermission })
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("got %v", err)
	}
}

func TestForSaveRestoresFileValue(t *testing.T) {
	t.Parallel()
	cfg := loadWith(t, map[string]string{"WRITEFREELY_SMTP_PASSWORD": "env"})
	if got := cfg.ForSave().Email.Password; got != "file" {
		t.Fatalf("ForSave: got %q, want file", got)
	}
	if cfg.Email.Password != "env" {
		t.Fatalf("ForSave changed the running config: %q", cfg.Email.Password)
	}
	out := filepath.Join(t.TempDir(), "out.ini")
	if err := Save(cfg, out); err != nil {
		t.Fatal(err)
	}
	re, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if re.Email.Password != "file" {
		t.Fatalf("saved password %q, want file", re.Email.Password)
	}
}

func TestForSaveRestoresEmptyFileValue(t *testing.T) {
	t.Parallel()
	cfg := New()
	if err := cfg.ApplyEnv(envMap(map[string]string{"WRITEFREELY_MAILGUN_PRIVATE": "env"}), os.ReadFile); err != nil {
		t.Fatal(err)
	}
	if got := cfg.ForSave().Email.MailgunPrivate; got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestApplyEnvCoversEveryOAuthSecret(t *testing.T) {
	t.Parallel()
	env := map[string]string{}
	for _, n := range []string{"SLACK", "WRITEAS", "GITLAB", "GITEA", "GENERIC"} {
		env["WRITEFREELY_OAUTH_"+n+"_CLIENT_SECRET"] = strings.ToLower(n) + "-secret"
	}
	env["WRITEFREELY_MAILGUN_PRIVATE"] = "mg"
	cfg := New()
	if err := cfg.ApplyEnv(envMap(env), os.ReadFile); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		"SLACK":   cfg.SlackOauth.ClientSecret,
		"WRITEAS": cfg.WriteAsOauth.ClientSecret,
		"GITLAB":  cfg.GitlabOauth.ClientSecret,
		"GITEA":   cfg.GiteaOauth.ClientSecret,
		"GENERIC": cfg.GenericOauth.ClientSecret,
	}
	for n, v := range got {
		if v != strings.ToLower(n)+"-secret" {
			t.Errorf("%s: got %q", n, v)
		}
	}
	if cfg.Email.MailgunPrivate != "mg" {
		t.Errorf("mailgun: got %q", cfg.Email.MailgunPrivate)
	}
	if len(SecretEnv) != 7 {
		t.Errorf("SecretEnv has %d entries, want 7", len(SecretEnv))
	}
}
