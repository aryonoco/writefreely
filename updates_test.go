package writefreely

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/writefreely/writefreely/config"
)

func TestUpdatesRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "v0.17.4")
	}))
	defer srv.Close()
	prevURL := versionURL
	versionURL = srv.URL
	defer func() { versionURL = prevURL }()

	cache := newUpdatesCache(defaultUpdatesCacheTime)
	t.Run("New Updates Cache", func(t *testing.T) {

		if cache == nil {
			t.Fatal("Returned nil cache")
		}

		if cache.frequency != defaultUpdatesCacheTime {
			t.Fatalf("Got cache expiry frequency: %s but expected: %s", cache.frequency, defaultUpdatesCacheTime)
		}

		if got := cache.CurrentVersion(); got != "v"+softwareVer {
			t.Fatalf("Got current version: %s but expected: %s", got, "v"+softwareVer)
		}
	})

	t.Run("Release URL", func(t *testing.T) {
		// the initial background check fetches the version; wait for it
		deadline := time.Now().Add(5 * time.Second)
		for cache.LatestVersion() == "" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		url := cache.ReleaseNotesURL()

		if want := "https://blog.writefreely.org/version-0-17-4"; url != want {
			t.Fatalf("Got release notes URL: %s but expected: %s", url, want)
		}

		reg, err := regexp.Compile(`^https:\/\/blog.writefreely.org\/version(-\d+){1,}$`)
		if err != nil {
			t.Fatalf("Test Case Error: Failed to compile regex: %v", err)
		}
		match := reg.MatchString(url)

		if !match {
			t.Fatalf("Malformed Release URL: %s", url)
		}
	})

	t.Run("Check Now", func(t *testing.T) {
		// ensure time between init and next check
		time.Sleep(1 * time.Second)

		prevLastCheck := cache.LastCheck()

		// force to known older version for latest and current
		prevLatestVer := "v0.8.1"
		cache.mu.Lock()
		cache.latestVersion = prevLatestVer
		cache.currentVersion = "v0.8.0"
		cache.mu.Unlock()

		err := cache.CheckNow()
		if err != nil {
			t.Fatalf("Error should be nil, got: %v", err)
		}

		lastCheck := cache.LastCheck()
		if prevLastCheck.Equal(lastCheck) {
			t.Fatal("Expected lastCheck to update")
		}

		if lastCheck.Before(prevLastCheck) {
			t.Fatal("Last check should be newer than previous")
		}

		if prevLatestVer == cache.LatestVersion() {
			t.Fatal("expected latestVersion to update")
		}

	})

	t.Run("Are Available", func(t *testing.T) {
		if !cache.AreAvailable() {
			t.Fatalf("Cache reports not updates but Current is %s and Latest is %s", cache.CurrentVersion(), cache.LatestVersion())
		}
	})

	t.Run("Latest Version", func(t *testing.T) {
		gotLatest := cache.LatestVersion()
		if gotLatest != "v0.17.4" {
			t.Fatalf("Malformed latest version. Expected: %s but got: %s", "v0.17.4", gotLatest)
		}
	})
}

func TestAdminUpdatesPageDuringCheck(t *testing.T) { //nolint:paralleltest // swaps the package-level versionURL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "v0.17.4")
	}))
	defer srv.Close()
	prevURL := versionURL
	versionURL = srv.URL
	defer func() { versionURL = prevURL }()

	app, _ := newTemplateTestApp(t, func(cfg *config.Config) {
		cfg.App.UpdateChecks = true
	})
	app.updates = &updatesCache{
		frequency:      defaultUpdatesCacheTime,
		currentVersion: "v" + softwareVer,
	}
	u := &User{Username: "admin"}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = app.updates.CheckNow()
		}()
		go func() {
			defer wg.Done()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/updates", http.NoBody)
			if err := handleViewAdminUpdates(app, u, httptest.NewRecorder(), req); err != nil {
				t.Errorf("handleViewAdminUpdates: %v", err)
			}
		}()
	}
	wg.Wait()
}
