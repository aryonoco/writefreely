/*
 * Copyright © 2019-2020 Musing Studio LLC.
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
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/writeas/web-core/log"
)

// updatesCacheTime is the default interval between cache updates for new
// software versions
const defaultUpdatesCacheTime = 12 * time.Hour

// updatesCache holds data about current and new releases of the writefreely
// software
type updatesCache struct {
	mu             sync.Mutex
	frequency      time.Duration
	lastCheck      time.Time
	latestVersion  string
	currentVersion string
	checkError     error
}

// CheckNow asks for the latest released version of writefreely and updates
// the cache last checked time. If the version postdates the current 'latest'
// the version value is replaced.
func (uc *updatesCache) CheckNow() error {
	if debugging {
		log.Info("[update check] Checking for update now.")
	}
	uc.mu.Lock()
	uc.lastCheck = time.Now()
	uc.mu.Unlock()

	// Fetch without holding the lock, so readers never wait on the network
	latestRemote, err := newVersionCheck()

	uc.mu.Lock()
	defer uc.mu.Unlock()
	if err != nil {
		log.Error("[update check] Failed: %v", err)
		uc.checkError = err
		return err
	}
	if CompareSemver(latestRemote, uc.latestVersion) == 1 {
		uc.latestVersion = latestRemote
	}
	return nil
}

// AreAvailable updates the cache if the frequency duration has passed
// then returns if the latest release is newer than the current running version.
func (uc *updatesCache) AreAvailable() bool {
	uc.mu.Lock()
	stale := time.Since(uc.lastCheck) > uc.frequency
	uc.mu.Unlock()
	if stale {
		uc.CheckNow()
	}
	return uc.AreAvailableNoCheck()
}

// AreAvailableNoCheck returns if the latest release is newer than the current
// running version.
func (uc *updatesCache) AreAvailableNoCheck() bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return CompareSemver(uc.latestVersion, uc.currentVersion) == 1
}

// LatestVersion returns the latest stored version available.
func (uc *updatesCache) LatestVersion() string {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.latestVersion
}

// CurrentVersion returns the version of the running software.
func (uc *updatesCache) CurrentVersion() string {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.currentVersion
}

// LastCheck returns the time of the most recent update check.
func (uc *updatesCache) LastCheck() time.Time {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.lastCheck
}

// CheckError returns the error from the most recent failed check, if any.
func (uc *updatesCache) CheckError() error {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.checkError
}

func (uc *updatesCache) ReleaseURL() string {
	return "https://writefreely.org/releases/" + uc.LatestVersion()
}

// ReleaseNotesURL returns the full URL to the blog.writefreely.org release notes
// for the latest version as stored in the cache.
func (uc *updatesCache) ReleaseNotesURL() string {
	return wfReleaseNotesURL(uc.LatestVersion())
}

func wfReleaseNotesURL(v string) string {
	ver := strings.TrimPrefix(v, "v")
	ver = strings.TrimSuffix(ver, ".0")
	// hack until go 1.12 in build/travis
	seg := strings.Split(ver, ".")
	return "https://blog.writefreely.org/version-" + strings.Join(seg, "-")
}

// newUpdatesCache returns an initialized updates cache
func newUpdatesCache(expiry time.Duration) *updatesCache {
	cache := updatesCache{
		frequency:      expiry,
		currentVersion: "v" + softwareVer,
	}
	go cache.CheckNow()
	return &cache
}

// InitUpdates initializes the updates cache, if the config value is set
// It uses the defaultUpdatesCacheTime for the cache expiry
func (app *App) InitUpdates() {
	if app.cfg.App.UpdateChecks {
		app.updates = newUpdatesCache(defaultUpdatesCacheTime)
	}
}

// versionURL is where the latest released version is fetched from.
var versionURL = "https://version.writefreely.org"

func newVersionCheck() (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, versionURL, http.NoBody)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if debugging {
		log.Info("[update check] GET %s", versionURL)
	}
	// TODO: return error if statusCode != OK
	if err == nil && res.StatusCode == http.StatusOK {
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}
	return "", err
}
