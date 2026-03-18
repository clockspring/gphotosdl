package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
)

// Serve the root page
func (g *Gphotos) getRoot(w http.ResponseWriter, r *http.Request) {
	slog.Info("got / request")
	_, _ = io.WriteString(w, `
<!DOCTYPE html>
<html lang="en">

<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>`+program+`</title>
  <link rel="stylesheet" href="styles.css">
</head>

<body>
  <h1>`+program+`</h1>
  <p>`+program+` is used to download full resolution Google Photos in combination with rclone.</p>
</body>

</html>`)
}

// Serve a photo ID
func (g *Gphotos) getID(w http.ResponseWriter, r *http.Request) {
	photoID := r.PathValue("photoID")
	slog.Info("got photo request", "id", photoID)
	path, err := g.Download(photoID)
	if err != nil {
		slog.Error("Download image failed", "id", photoID, "err", err)
		var h httpError
		if errors.As(err, &h) {
			w.WriteHeader(int(h))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	slog.Info("Downloaded photo", "id", photoID, "path", path)

	// Remove the file after it has been served
	defer func() {
		err := os.Remove(path)
		if err == nil {
			slog.Debug("Removed downloaded photo", "id", photoID, "path", path)
		} else {
			slog.Error("Failed to remove download directory", "id", photoID, "path", path, "err", err)
		}
	}()

	http.ServeFile(w, r, path)
}

