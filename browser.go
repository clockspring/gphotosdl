package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// start the browser off and check it is authenticated
func (g *Gphotos) startBrowser() error {
	// We use the default profile in our new data directory
	l := launcher.New().
		Bin(browserPath).
		Headless(!*show).
		UserDataDir(browserConfig).
		Preferences(browserPrefs).
		Delete("use-mock-keychain").
		Set("disable-gpu").
		Set("disable-audio-output").
		Logger(logger{})

	slog.Debug(fmt.Sprintf("Launcher command line: %q", l.FormatArgs()))

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("browser launch: %w", err)
	}

	g.browser = rod.New().
		ControlURL(url).
		NoDefaultDevice().
		Trace(true).
		SlowMotion(100 * time.Millisecond).
		Logger(logger{})

	err = g.browser.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to browser: %w", err)
	}

	g.page, err = g.browser.Page(proto.TargetCreateTarget{URL: gphotosURL})
	if err != nil {
		return fmt.Errorf("couldn't open gphotos URL: %w", err)
	}

	eventCallback := func(e *proto.PageLifecycleEvent) {
		slog.Debug("Event", "Name", e.Name, "Dump", e)
	}
	g.page.EachEvent(eventCallback)

	err = g.page.WaitLoad()
	if err != nil {
		return fmt.Errorf("gphotos page load: %w", err)
	}

	authenticated := false
	for range 60 {
		time.Sleep(1 * time.Second)
		info := g.page.MustInfo()
		slog.Debug("URL", "url", info.URL)
		// When not authenticated Google redirects away from the Photos URL
		if info.URL == gphotosURL {
			authenticated = true
			slog.Debug("Authenticated")
			break
		}
		slog.Debug(fmt.Sprintf("Current URL: %s", info.URL))
		slog.Info("Please log in, or re-run with -login flag")
	}
	if !authenticated {
		return errors.New("browser is not log logged in - rerun with the -login flag")
	}
	return nil
}

// Download a photo with the ID given
//
// Returns the path to the photo which should be deleted after use
func (g *Gphotos) Download(photoID string) (string, error) {
	// Can only download one picture at once
	g.mu.Lock()
	defer g.mu.Unlock()
	url := gphotoURL + photoID

	slog := slog.With("id", photoID)

	// Create a new blank browser tab
	slog.Error("Open new tab")
	page, err := g.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", fmt.Errorf("failed to open browser tab for photo %q: %w", photoID, err)
	}
	defer func() {
		err := page.Close()
		if err != nil {
			slog.Error("Error closing tab", "Error", err)
		}
	}()

	var netResponse *proto.NetworkResponseReceived

	// Check the correct network request is received
	waitNetwork := page.EachEvent(func(e *proto.NetworkResponseReceived) bool {
		slog.Debug("network response", "rxURL", e.Response.URL, "status", e.Response.Status)
		if strings.HasPrefix(e.Response.URL, gphotoURLReal) {
			netResponse = e
			return true
		} else if strings.HasPrefix(e.Response.URL, gphotoURLLegacy) {
			netResponse = e
			return true
		}
		return false
	})

	// Navigate to the photo URL
	slog.Debug("Navigate to photo URL")
	err = page.Navigate(url)
	if err != nil {
		return "", fmt.Errorf("failed to navigate to photo %q: %w", photoID, err)
	}
	slog.Debug("Wait for page to load")
	err = g.page.WaitLoad()
	if err != nil {
		return "", fmt.Errorf("gphoto page load: %w", err)
	}

	// Wait for the photos network request to happen
	slog.Debug("Wait for network response")
	waitNetwork()

	// Print request headers
	if netResponse.Response.Status != 200 {
		return "", fmt.Errorf("gphoto fetch failed: %w", httpError(netResponse.Response.Status))
	}

	// Download waiter
	wait := g.browser.WaitDownload(downloadDir)

	// Urg doesn't always catch the keypress so wait
	time.Sleep(time.Second)

	// Shift-D to download
	page.KeyActions().Press(input.ShiftLeft).Type('D').MustDo()

	// Wait for download
	slog.Debug("Wait for download")
	info := wait()
	path := filepath.Join(downloadDir, info.GUID)

	// Check file
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	slog.Debug("Download successful", "size", fi.Size(), "path", path)

	return path, nil
}

// Close the browser
func (g *Gphotos) Close() {
	err := g.browser.Close()
	if err == nil {
		slog.Debug("Closed browser")
	} else {
		slog.Error("Failed to close browser", "err", err)
	}
}
