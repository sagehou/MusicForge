package forge

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Conditional reads still use a fresh database snapshot, including job/repair
// status. The browser can reuse unchanged data without transferring or parsing it.
func respondLibrary(w http.ResponseWriter, r *http.Request, list []Source) {
	data, err := json.Marshal(list)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	etag := `W/"` + digest(string(data)) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Add("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", "application/json")
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if len(data) > 1024 && acceptsLibraryGzip(r.Header.Get("Accept-Encoding")) {
		var compressed bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		_, _ = writer.Write(data)
		_ = writer.Close()
		data = compressed.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	}
	http.ServeContent(w, r, "library.json", time.Time{}, bytes.NewReader(data))
}

func acceptsLibraryGzip(header string) bool {
	for _, encoding := range strings.Split(header, ",") {
		name, params, err := mime.ParseMediaType(strings.TrimSpace(encoding))
		if err != nil || name != "gzip" {
			continue
		}
		if value, ok := params["q"]; ok {
			quality, err := strconv.ParseFloat(value, 64)
			return err == nil && quality > 0 && quality <= 1
		}
		return true
	}
	return false
}
