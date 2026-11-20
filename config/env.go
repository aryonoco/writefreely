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
	"fmt"
	"os"
	"strings"
)

// SecretEnv lists the secrets that may be supplied through the environment.
// Each is read from the variable Name, or from the file named by Name+"_FILE".
var SecretEnv = []struct {
	Name  string
	Field func(*Config) *string
}{
	{"WRITEFREELY_SMTP_PASSWORD", func(c *Config) *string { return &c.Email.Password }},
	{"WRITEFREELY_MAILGUN_PRIVATE", func(c *Config) *string { return &c.Email.MailgunPrivate }},
	{"WRITEFREELY_OAUTH_SLACK_CLIENT_SECRET", func(c *Config) *string { return &c.SlackOauth.ClientSecret }},
	{"WRITEFREELY_OAUTH_WRITEAS_CLIENT_SECRET", func(c *Config) *string { return &c.WriteAsOauth.ClientSecret }},
	{"WRITEFREELY_OAUTH_GITLAB_CLIENT_SECRET", func(c *Config) *string { return &c.GitlabOauth.ClientSecret }},
	{"WRITEFREELY_OAUTH_GITEA_CLIENT_SECRET", func(c *Config) *string { return &c.GiteaOauth.ClientSecret }},
	{"WRITEFREELY_OAUTH_GENERIC_CLIENT_SECRET", func(c *Config) *string { return &c.GenericOauth.ClientSecret }},
}

// ApplyEnv overrides the secrets in SecretEnv with the values of their
// environment variables, or with the contents of the files named by their
// _FILE variables. It is an error to set both a variable and its _FILE twin.
// The value each overridden field held before is remembered, so that ForSave
// can restore it.
func (cfg *Config) ApplyEnv(getenv func(string) string, readFile func(string) ([]byte, error)) error {
	for _, s := range SecretEnv {
		val := getenv(s.Name)
		path := getenv(s.Name + "_FILE")
		if val == "" && path == "" {
			continue
		}
		if val != "" && path != "" {
			return fmt.Errorf("%s and %s_FILE are both set; set only one", s.Name, s.Name)
		}
		if path != "" {
			b, err := readFile(path)
			if err != nil {
				return fmt.Errorf("%s_FILE: reading %s: %w (the file must be readable by the server's user, uid %d)", s.Name, path, err, os.Getuid())
			}
			val = string(b)
			if strings.HasSuffix(val, "\n") {
				val = strings.TrimSuffix(strings.TrimSuffix(val, "\n"), "\r")
			}
		}
		f := s.Field(cfg)
		if cfg.fromEnv == nil {
			cfg.fromEnv = map[string]string{}
		}
		if _, seen := cfg.fromEnv[s.Name]; !seen {
			cfg.fromEnv[s.Name] = *f
		}
		*f = val
	}
	return nil
}

// ForSave returns a copy of the Config in which every field set from the
// environment holds the value it had in the configuration file. The Config
// itself is not changed.
func (cfg *Config) ForSave() *Config {
	c := *cfg
	for _, s := range SecretEnv {
		if v, ok := cfg.fromEnv[s.Name]; ok {
			*s.Field(&c) = v
		}
	}
	return &c
}
