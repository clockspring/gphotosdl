// Package main implements gphotosdl
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

const (
	program         = "gphotosdl"
	gphotosURL      = "https://photos.google.com/"
	loginURL        = "https://photos.google.com/login"
	gphotoURLReal   = "https://photos.google.com/photo/"
	gphotoURLLegacy = "https://photos.google.com/lr/photo/" // redirects to gphotosURLReal which uses a different ID
)

// Flags
var (
	debug     = flag.Bool("debug", false, "set to see debug messages")
	login     = flag.Bool("login", false, "set to launch login browser")
	show      = flag.Bool("show", false, "set to show the browser (not headless)")
	addr      = flag.String("addr", "localhost:8282", "address for the web server")
	legacyIDs = flag.Bool("legacy-ids", false, fmt.Sprintf("use legacy IDs to get photos (using the base URL %s)", gphotoURLLegacy))
	useJSON   = flag.Bool("json", false, "log in JSON format")
)

// Global variables
var (
	configRoot    string      // top level config dir, typically ~/.config/gphotodl
	browserConfig string      // work directory for browser instance
	browserPath   string      // path to the browser binary
	downloadDir   string      // temporary directory for downloads
	browserPrefs  string      // JSON config for the browser
	version       = "DEV"     // set by goreleaser
	commit        = "NONE"    // set by goreleaser
	date          = "UNKNOWN" // set by goreleaser
	gphotoURL     string      // Base URL for photos (either gphotoURLLegacy or gphotoURLReal)
)

// Remove the download directory and contents
func removeDownloadDirectory() {
	if downloadDir == "" {
		return
	}
	err := os.RemoveAll(downloadDir)
	if err == nil {
		slog.Debug("Removed download directory")
	} else {
		slog.Error("Failed to remove download directory", "err", err)
	}
}

// Set up the global variables from the flags
func config() (err error) {
	version := fmt.Sprintf("%s version %s, commit %s, built at %s", program, version, commit, date)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\n", version)
	}
	flag.Parse()

	// Set up the logger
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	if *useJSON {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		slog.SetDefault(logger)
	} else {
		slog.SetLogLoggerLevel(level) // set log level of Default Handler
	}
	slog.Debug(version)

	if *legacyIDs {
		gphotoURL = gphotoURLLegacy
	} else {
		gphotoURL = gphotoURLReal
	}
	slog.Debug(fmt.Sprintf("Using base URL for photos: %s", gphotoURL))

	configRoot, err = os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("didn't find config directory: %w", err)
	}
	configRoot = filepath.Join(configRoot, program)
	browserConfig = filepath.Join(configRoot, "browser")
	err = os.MkdirAll(browserConfig, 0700)
	if err != nil {
		return fmt.Errorf("config directory creation: %w", err)
	}
	slog.Debug("Configured config", "config_root", configRoot, "browser_config", browserConfig)

	downloadDir, err = os.MkdirTemp("", program)
	if err != nil {
		log.Fatal(err)
	}
	slog.Debug("Created download directory", "download_directory", downloadDir)

	// Find the browser
	var ok bool
	browserPath, ok = launcher.LookPath()
	if !ok {
		return errors.New("browser not found")
	}
	slog.Debug("Found browser", "browser_path", browserPath)

	// Browser preferences
	pref := map[string]any{
		"download": map[string]any{
			"default_directory": "/tmp/gphotos", // FIXME
		},
	}
	prefJSON, err := json.Marshal(pref)
	if err != nil {
		return fmt.Errorf("failed to make preferences: %w", err)
	}
	browserPrefs = string(prefJSON)
	slog.Debug("made browser preferences", "prefs", browserPrefs)

	return nil
}

// logger makes an io.Writer from slog.Debug
type logger struct{}

// Write writes len(p) bytes from p to the underlying data stream.
func (logger) Write(p []byte) (n int, err error) {
	s := string(p)
	s = strings.TrimSpace(s)
	slog.Debug(s)
	return len(p), nil
}

// Println is called to log text
func (logger) Println(vs ...any) {
	s := fmt.Sprint(vs...)
	s = strings.TrimSpace(s)
	slog.Debug(s)
}

// Gphotos is a single page browser for Google Photos
type Gphotos struct {
	browser *rod.Browser
	page    *rod.Page
	mu      sync.Mutex // only one download at once is allowed
}

// New creates a new browser on the gphotos main page to check we are logged in
func New() (*Gphotos, error) {
	g := &Gphotos{}
	err := g.startBrowser()
	if err != nil {
		return nil, err
	}
	err = g.startServer()
	if err != nil {
		return nil, err
	}
	return g, nil
}

// httpError wraps an HTTP status code
type httpError int

func (h httpError) Error() string {
	return fmt.Sprintf("HTTP Error %d", h)
}

func main() {
	err := config()
	if err != nil {
		slog.Error("Configuration failed", "err", err)
		os.Exit(2)
	}
	defer removeDownloadDirectory()

	// If login is required, run the browser standalone
	if *login {
		slog.Info("Log in to google with the browser that pops up, close it, then re-run this without the -login flag")
		cmd := exec.Command(
			browserPath,
			"--no-default-browser-check",
			"--no-first-run",
			"--disable-sync",
			"--auto-accept-browser-signin-for-tests",
			"--disable-default-apps",
			"--user-data-dir="+browserConfig,
			loginURL,
		)
		err = cmd.Start()
		if err != nil {
			slog.Error("Failed to start browser", "err", err)
			os.Exit(2)
		}
		slog.Info("Waiting for browser to be closed")
		err = cmd.Wait()
		if err != nil {
			slog.Error("Browser run failed", "err", err)
			os.Exit(2)
		}
		slog.Info("Now restart this program without -login")
		os.Exit(0)
	}

	g, err := New()
	if err != nil {
		slog.Error("Failed to make browser", "err", err)
		os.Exit(2)
	}
	defer g.Close()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, exitSignals...)

	// Wait for CTRL-C or SIGTERM
	slog.Info("Press CTRL-C (or kill) to quit")
	sig := <-quit
	slog.Info("Signal received - shutting down", "signal", sig)
}
