package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
)

// start the web server off
func (g *Gphotos) startServer() error {
	http.HandleFunc("GET /", g.getRoot)
	http.HandleFunc("GET /id/{photoID}", g.getID)

	go func() {
		err := http.ListenAndServe(*addr, nil)
		if errors.Is(err, http.ErrServerClosed) {
			slog.Debug("web server closed")
		} else if err != nil {
			slog.Error("Error starting web server", "err", err)
			os.Exit(1)
		}
	}()
	return nil
}
