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

// jsHandler serves the page's scripts, GET and HEAD /js/<path>, from the js
// directory of an fs.FS whose files never change, such as the binary's
// embedded ones.
//
// A browser revalidates each script on every load (no-cache) against its
// content ETag: an unchanged script is not downloaded again, and a changed
// one is, so a page served after a deploy runs that deploy's scripts.
type jsHandler struct {
	fsys fs.FS

	mu    sync.Mutex
	files map[string]*jsFile // by name in fsys, read on first request (guarded by mu)
}

// jsFile is a script's content and the ETag computed from it.
type jsFile struct {
	data []byte
	etag string
}

// newJSHandler returns the handler serving /js/<path> from fsys's js
// directory, for mounting on "/js/".
func newJSHandler(fsys fs.FS) http.Handler {
	return &jsHandler{fsys: fsys, files: map[string]*jsFile{}}
}

func (h *jsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, mounted := strings.CutPrefix(r.URL.Path, "/js/")
	if !mounted || !fs.ValidPath(rel) || !strings.HasSuffix(rel, ".js") {
		http.NotFound(w, r)
		return
	}
	f, err := h.file(path.Join("js", rel))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("serve %s: %v", r.URL.Path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", f.etag)
	// ServeContent answers a matching If-None-Match with 304, HEAD without
	// a body and Range with the part asked for. It needs no name to deduce
	// the type from: the type set above does not depend on the host's MIME
	// tables, which may map .js to another type.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}

// file returns the named script, read and given its ETag on its first
// request. A directory does not exist as a script.
func (h *jsHandler) file(name string) (*jsFile, error) {
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
	f := &jsFile{data: data, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
	h.files[name] = f
	return f, nil
}
