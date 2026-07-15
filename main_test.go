package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream is a controllable stand-in for mise-versions.jdx.dev.
type fakeUpstream struct {
	mu       sync.Mutex
	hits     int
	body     []byte
	etag     string
	status   int    // 0 => 200
	cacheCtl string // Cache-Control header value, if any
}

func (f *fakeUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits++
		f.mu.Unlock()
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			w.Write([]byte("upstream says no"))
			return
		}
		if f.etag != "" && r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if f.etag != "" {
			w.Header().Set("ETag", f.etag)
		}
		if f.cacheCtl != "" {
			w.Header().Set("Cache-Control", f.cacheCtl)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(f.body)
	}
}

func (f *fakeUpstream) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

const testPath = "/api/github/repos/o/r/releases/latest"

func newTestMirror(t *testing.T, up *fakeUpstream, ttl time.Duration) (*mirror, *httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(up.handler())
	dir := t.TempDir()
	return newMirror(srv.URL, dir, ttl), srv, dir
}

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestMirrorPathsStayInsideCacheDir(t *testing.T) {
	dir := "/var/cache"
	// Adversarial inputs must never map outside the cache dir.
	for _, in := range []string{
		"/api/github/../../etc/passwd",
		"/../../../etc/shadow",
		"/api/github/repos/o/r/releases/latest",
	} {
		body, meta, ok := mirrorPaths(dir, in)
		if !ok {
			continue
		}
		if !strings.HasPrefix(body, dir+string(os.PathSeparator)) {
			t.Errorf("mirrorPaths(%q) escaped cache dir: %q", in, body)
		}
		if meta != body+".meta" {
			t.Errorf("mirrorPaths(%q) meta=%q, want %q", in, meta, body+".meta")
		}
	}
	// Root-only paths have nothing to cache.
	for _, in := range []string{"/", "", "/.."} {
		if _, _, ok := mirrorPaths(dir, in); ok {
			t.Errorf("mirrorPaths(%q) unexpectedly ok", in)
		}
	}
	// A relative cache dir (the repo-root loop uses ".") must still map normally.
	for _, rel := range []string{".", "cache", "./tree"} {
		body, _, ok := mirrorPaths(rel, "/api/github/repos/o/r/releases/latest")
		if !ok {
			t.Errorf("mirrorPaths(%q, ...) rejected a valid path", rel)
			continue
		}
		want := filepath.Join(rel, "api/github/repos/o/r/releases/latest")
		if body != want {
			t.Errorf("mirrorPaths(%q, ...) body=%q, want %q", rel, body, want)
		}
	}
}

func TestMirrorMissThenHit(t *testing.T) {
	up := &fakeUpstream{body: []byte(`{"tag_name":"v1"}`), etag: `"abc"`}
	m, srv, dir := newTestMirror(t, up, time.Hour)
	defer srv.Close()

	r1 := get(t, m, http.MethodGet, testPath)
	if r1.Code != 200 || r1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request: code=%d X-Cache=%q, want 200 MISS", r1.Code, r1.Header().Get("X-Cache"))
	}
	if got := r1.Body.String(); got != string(up.body) {
		t.Fatalf("served body %q != upstream %q", got, up.body)
	}

	// Body must be recorded at the path-mirrored location, servable as-is.
	bodyFile := filepath.Join(dir, "api/github/repos/o/r/releases/latest")
	onDisk, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatalf("expected body at %s: %v", bodyFile, err)
	}
	if string(onDisk) != string(up.body) {
		t.Fatalf("on-disk body %q != upstream %q (must equal for static hosting)", onDisk, up.body)
	}
	if _, err := os.Stat(bodyFile + ".meta"); err != nil {
		t.Fatalf("expected meta sidecar beside body: %v", err)
	}

	r2 := get(t, m, http.MethodGet, testPath)
	if r2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache=%q, want HIT", r2.Header().Get("X-Cache"))
	}
	if up.hitCount() != 1 {
		t.Fatalf("upstream hit %d times, want 1 (HIT must not refetch)", up.hitCount())
	}
}

func TestMirrorRevalidated(t *testing.T) {
	up := &fakeUpstream{body: []byte(`{"tag_name":"v1"}`), etag: `"abc"`}
	// ttl ~0 forces the cached entry stale so the next request revalidates.
	m, srv, _ := newTestMirror(t, up, time.Nanosecond)
	defer srv.Close()

	get(t, m, http.MethodGet, testPath) // MISS, populates cache
	r2 := get(t, m, http.MethodGet, testPath)
	if r2.Header().Get("X-Cache") != "REVALIDATED" {
		t.Fatalf("X-Cache=%q, want REVALIDATED", r2.Header().Get("X-Cache"))
	}
	if r2.Body.String() != string(up.body) {
		t.Fatalf("revalidated body %q != %q", r2.Body.String(), up.body)
	}
	if up.hitCount() != 2 {
		t.Fatalf("upstream hits=%d, want 2 (miss + 304)", up.hitCount())
	}
}

func TestMirrorStaleWhenUpstreamDown(t *testing.T) {
	up := &fakeUpstream{body: []byte(`{"tag_name":"v1"}`), etag: `"abc"`}
	m, srv, _ := newTestMirror(t, up, time.Nanosecond)

	get(t, m, http.MethodGet, testPath) // MISS, populates cache
	srv.Close()                         // upstream now unreachable

	r2 := get(t, m, http.MethodGet, testPath)
	if r2.Header().Get("X-Cache") != "STALE" {
		t.Fatalf("X-Cache=%q, want STALE", r2.Header().Get("X-Cache"))
	}
	if r2.Body.String() != string(up.body) {
		t.Fatalf("stale body %q != %q", r2.Body.String(), up.body)
	}
}

func TestMirrorBypassNotCached(t *testing.T) {
	up := &fakeUpstream{status: http.StatusNotFound}
	m, srv, dir := newTestMirror(t, up, time.Hour)
	defer srv.Close()

	r := get(t, m, http.MethodGet, testPath)
	if r.Code != http.StatusNotFound || r.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("code=%d X-Cache=%q, want 404 BYPASS", r.Code, r.Header().Get("X-Cache"))
	}
	if _, err := os.Stat(filepath.Join(dir, "api/github/repos/o/r/releases/latest")); !os.IsNotExist(err) {
		t.Fatalf("404 response must not be cached")
	}
}

func TestMirrorBadGatewayNoCache(t *testing.T) {
	up := &fakeUpstream{body: []byte(`x`)}
	m, srv, _ := newTestMirror(t, up, time.Hour)
	srv.Close() // unreachable, and nothing cached yet

	r := get(t, m, http.MethodGet, testPath)
	if r.Code != http.StatusBadGateway {
		t.Fatalf("code=%d, want 502", r.Code)
	}
}

func TestMirrorMethodNotAllowed(t *testing.T) {
	up := &fakeUpstream{body: []byte(`x`)}
	m, srv, _ := newTestMirror(t, up, time.Hour)
	defer srv.Close()

	r := get(t, m, http.MethodPost, testPath)
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want 405", r.Code)
	}
	if r.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow=%q, want \"GET, HEAD\"", r.Header().Get("Allow"))
	}
}
