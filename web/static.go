package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// staticHandler serves the files of one kind, GET and HEAD /<dir>/<path>,
// from the dir directory of an fs.FS whose files never change, such as the
// binary's embedded ones: the page's scripts (newJSHandler) and the data it
// reads (newDataHandler).
//
// A browser revalidates each file on every load (no-cache) against its
// content ETag: an unchanged file is not downloaded again, and a changed
// one is, so a page served after a deploy runs that deploy's scripts and
// reads its data.
type staticHandler struct {
	fsys        fs.FS
	dir         string // the directory served, and the path it is mounted on
	suffix      string // the name's ending of the files of its kind
	contentType string

	mu    sync.Mutex
	files map[string]*staticFile // by name in fsys, read on first request (guarded by mu)
}

// staticFile is a file's content and the ETag computed from it.
type staticFile struct {
	data []byte
	etag string
}

// newJSHandler returns the handler serving the page's scripts, /js/<path>.js
// from fsys's js directory, for mounting on "/js/".
func newJSHandler(fsys fs.FS) http.Handler {
	return &staticHandler{fsys: fsys, dir: "js", suffix: ".js", contentType: "text/javascript; charset=utf-8", files: map[string]*staticFile{}}
}

// newDataHandler returns the handler serving the data the page reads,
// /data/<path>.json from fsys's data directory, for mounting on "/data/".
func newDataHandler(fsys fs.FS) http.Handler {
	return &staticHandler{fsys: fsys, dir: "data", suffix: ".json", contentType: "application/json", files: map[string]*staticFile{}}
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, mounted := strings.CutPrefix(r.URL.Path, "/"+h.dir+"/")
	if !mounted || !fs.ValidPath(rel) || !strings.HasSuffix(rel, h.suffix) {
		http.NotFound(w, r)
		return
	}
	f, err := h.file(path.Join(h.dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("serve %s: %v", r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", h.contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", f.etag)
	// ServeContent answers a matching If-None-Match with 304, HEAD without
	// a body and Range with the part asked for. It needs no name to deduce
	// the type from: the type set above does not depend on the host's MIME
	// tables, which may map .js to another type.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}

// file returns the named file, read and given its ETag on its first
// request. A directory does not exist as a file.
func (h *staticHandler) file(name string) (*staticFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f, ok := h.files[name]; ok {
		return f, nil
	}
	info, err := fs.Stat(h.fsys, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		return nil, err
	}
	// Half the hash, 128 bits, tells a file's versions apart.
	sum := sha256.Sum256(data)
	f := &staticFile{data: data, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
	h.files[name] = f
	return f, nil
}
