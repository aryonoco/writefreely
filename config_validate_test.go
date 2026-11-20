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
	"strings"
	"testing"

	"github.com/writefreely/writefreely/config"
)

func TestValidateConfigRefusesBothSMTPTLSModes(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Email: config.EmailCfg{
		Host: "smtp.example.com", Port: 587, Username: "u", Password: "p",
		EnableTLS: true, EnableStartTLS: true,
	}}
	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("expected startup validation to refuse both TLS modes")
	}
	for _, key := range []string{"smtp_enable_tls", "smtp_enable_start_tls"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}

func TestValidateConfigAcceptsMailgunWithStaleSMTPKeys(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Email: config.EmailCfg{
		Domain: "mg.example.com", MailgunPrivate: "key",
		Host: "smtp.example.com", Port: 587, Username: "u", Password: "p",
		EnableTLS: true, EnableStartTLS: true,
	}}
	if err := validateConfig(cfg); err != nil {
		t.Errorf("Mailgun configuration was refused: %v", err)
	}
}
