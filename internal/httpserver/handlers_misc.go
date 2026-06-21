package httpserver

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---------- /downloads/<file> ----------

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/downloads/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "\\") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cur().DownloadDir(), filepath.FromSlash(name))
	// 必须在 DownloadDir 下
	abs, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	base, err := filepath.Abs(s.cur().DownloadDir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	_, _ = io.Copy(w, f)
}
