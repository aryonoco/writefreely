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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/writefreely/writefreely/config"
)

func TestCreateUserRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := config.New()
	cfg.Database.Type = driverSQLite
	cfg.Database.FileName = filepath.Join(dir, "wf.db")
	cfgFile := filepath.Join(dir, "config.ini")
	require.NoError(t, config.Save(cfg, cfgFile))
	require.NoError(t, CreateSchema(NewApp(cfgFile)))

	// A regular user can't be created before the admin exists.
	err := CreateUser(NewApp(cfgFile), "alice", "a-long-enough-password", false)
	require.Error(t, err)
	assert.Equal(t, "no admin user exists yet; create the admin user first", err.Error())
	assert.NotContains(t, err.Error(), "[USER]:[PASSWORD]")

	require.NoError(t, CreateUser(NewApp(cfgFile), "owner", "a-long-enough-password", true))

	// A second admin can't be created.
	err = CreateUser(NewApp(cfgFile), "second", "a-long-enough-password", true)
	require.Error(t, err)
	assert.Equal(t, "an admin user already exists (owner); create a regular user instead", err.Error())
	assert.NotContains(t, err.Error(), "[USER]:[PASSWORD]")

	// Regular users are fine once the admin exists.
	assert.NoError(t, CreateUser(NewApp(cfgFile), "alice", "a-long-enough-password", false))
}
