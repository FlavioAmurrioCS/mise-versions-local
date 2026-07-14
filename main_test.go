package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// serveReq drives a handler with an in-memory request/response.
func serveReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(method, target, nil))
	return rr
}

func TestToolsGzHandler(t *testing.T) {
	dir := t.TempDir()
	toml := `[versions]
"cpython-3.8.1-x-install_only.tar.gz" = { created_at = 2022-01-01T00:00:00.000Z }
"cpython-3.9.1-x-install_only.tar.gz" = {}
`
	if err := os.WriteFile(filepath.Join(dir, "python-precompiled-x.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	h := toolsGzHandler(dir)

	rr := serveReq(h, http.MethodGet, "/tools/python-precompiled-x.gz")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q, want application/gzip", ct)
	}
	gz, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(gz)
	want := "cpython-3.8.1-x-install_only.tar.gz\ncpython-3.9.1-x-install_only.tar.gz\n"
	if string(got) != want {
		t.Fatalf("decompressed = %q, want %q", got, want)
	}

	if c := serveReq(h, http.MethodGet, "/tools/missing.gz").Code; c != http.StatusNotFound {
		t.Fatalf("missing tool status = %d, want 404", c)
	}
	if c := serveReq(h, http.MethodGet, "/tools/sub/dir.gz").Code; c != http.StatusNotFound {
		t.Fatalf("nested path status = %d, want 404", c)
	}
}

func TestMaxAgeSeconds(t *testing.T) {
	cases := map[string]int64{
		"max-age=3600":                        3600,
		"public, max-age=7200, s-maxage=3600": 7200,
		"no-store":                            0,
		"":                                    0,
		"max-age=notanumber":                  0,
	}
	for in, want := range cases {
		if got := maxAgeSeconds(in); got != want {
			t.Errorf("maxAgeSeconds(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFresh(t *testing.T) {
	p := &githubProxy{ttl: time.Hour}
	now := time.Now().Unix()
	cases := []struct {
		name string
		m    cacheMeta
		want bool
	}{
		{"within max-age", cacheMeta{FetchedAt: now, MaxAge: 3600}, true},
		{"past max-age", cacheMeta{FetchedAt: now - 7200, MaxAge: 3600}, false},
		{"no max-age within ttl", cacheMeta{FetchedAt: now, MaxAge: 0}, true},
		{"no max-age past ttl", cacheMeta{FetchedAt: now - 7200, MaxAge: 0}, false},
	}
	for _, c := range cases {
		if got := p.fresh(c.m); got != c.want {
			t.Errorf("%s: fresh = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "k.meta")
	bodyPath := filepath.Join(dir, "k.body")
	meta := cacheMeta{URL: "u", Status: 200, ContentType: "application/json", FetchedAt: 123, MaxAge: 60}
	body := []byte(`{"hello":"world"}`)

	if err := writeCache(metaPath, bodyPath, meta, body); err != nil {
		t.Fatal(err)
	}
	gotMeta, gotBody, ok := loadCache(metaPath, bodyPath)
	if !ok {
		t.Fatal("loadCache reported miss after write")
	}
	if gotMeta != meta {
		t.Errorf("meta = %+v, want %+v", gotMeta, meta)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(tmp) != 0 {
		t.Errorf("atomicWrite left temp files: %v", tmp)
	}
}

// fakeUpstream is a controllable mise-versions stand-in that counts requests.
type fakeUpstream struct {
	hits int32
	fn   func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.hits, 1)
		f.fn(w, r)
	})
}

func TestProxyMissThenHit(t *testing.T) {
	fu := &fakeUpstream{fn: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		io.WriteString(w, `{"ok":true}`)
	}}
	up := httptest.NewServer(fu.handler())
	defer up.Close()
	p := newGitHubProxy(up.URL, t.TempDir(), time.Hour)

	rr1 := serveReq(p, http.MethodGet, "/api/github/repos/o/r/releases/latest")
	if x := rr1.Header().Get("X-Cache"); x != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", x)
	}
	rr2 := serveReq(p, http.MethodGet, "/api/github/repos/o/r/releases/latest")
	if x := rr2.Header().Get("X-Cache"); x != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", x)
	}
	if rr2.Body.String() != `{"ok":true}` {
		t.Fatalf("HIT body = %q", rr2.Body.String())
	}
	if h := atomic.LoadInt32(&fu.hits); h != 1 {
		t.Fatalf("upstream hit %d times, want 1", h)
	}
}

func TestProxyRevalidated(t *testing.T) {
	fu := &fakeUpstream{fn: func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", "Tue, 14 Jul 2026 00:00:00 GMT")
		w.Header().Set("Cache-Control", "max-age=0") // force staleness next time
		io.WriteString(w, "body-v1")
	}}
	up := httptest.NewServer(fu.handler())
	defer up.Close()
	p := newGitHubProxy(up.URL, t.TempDir(), time.Nanosecond)

	if x := serveReq(p, http.MethodGet, "/api/github/x").Header().Get("X-Cache"); x != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", x)
	}
	rr := serveReq(p, http.MethodGet, "/api/github/x")
	if x := rr.Header().Get("X-Cache"); x != "REVALIDATED" {
		t.Fatalf("second X-Cache = %q, want REVALIDATED", x)
	}
	if rr.Body.String() != "body-v1" {
		t.Fatalf("revalidated body = %q, want body-v1", rr.Body.String())
	}
}

func TestProxyStaleWhenUpstreamDown(t *testing.T) {
	fu := &fakeUpstream{fn: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=0")
		io.WriteString(w, "cached-body")
	}}
	up := httptest.NewServer(fu.handler())
	p := newGitHubProxy(up.URL, t.TempDir(), time.Nanosecond)

	if x := serveReq(p, http.MethodGet, "/api/github/x").Header().Get("X-Cache"); x != "MISS" {
		t.Fatalf("seed X-Cache = %q, want MISS", x)
	}
	up.Close() // upstream now unreachable

	rr := serveReq(p, http.MethodGet, "/api/github/x")
	if x := rr.Header().Get("X-Cache"); x != "STALE" {
		t.Fatalf("X-Cache = %q, want STALE", x)
	}
	if rr.Body.String() != "cached-body" {
		t.Fatalf("stale body = %q", rr.Body.String())
	}
}

func TestProxyBypassNotCached(t *testing.T) {
	fu := &fakeUpstream{fn: func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such repo", http.StatusNotFound)
	}}
	up := httptest.NewServer(fu.handler())
	defer up.Close()
	dir := t.TempDir()
	p := newGitHubProxy(up.URL, dir, time.Hour)

	rr := serveReq(p, http.MethodGet, "/api/github/repos/no/repo/releases/tags/v1")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if x := rr.Header().Get("X-Cache"); x != "BYPASS" {
		t.Fatalf("X-Cache = %q, want BYPASS", x)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.meta")); len(entries) != 0 {
		t.Fatalf("404 should not be cached, found: %v", entries)
	}
}

func TestProxyBadGatewayNoCache(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := up.URL
	up.Close() // unreachable, and nothing cached
	p := newGitHubProxy(url, t.TempDir(), time.Hour)

	if c := serveReq(p, http.MethodGet, "/api/github/x").Code; c != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", c)
	}
}

func TestProxyMethodNotAllowed(t *testing.T) {
	p := newGitHubProxy("http://example.invalid", t.TempDir(), time.Hour)
	if c := serveReq(p, http.MethodPost, "/api/github/x").Code; c != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", c)
	}
}

func TestProxyConcurrentMissDedup(t *testing.T) {
	fu := &fakeUpstream{fn: func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond) // widen the race window
		w.Header().Set("Cache-Control", "max-age=3600")
		io.WriteString(w, "x")
	}}
	up := httptest.NewServer(fu.handler())
	defer up.Close()
	p := newGitHubProxy(up.URL, t.TempDir(), time.Hour)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveReq(p, http.MethodGet, "/api/github/same/key")
		}()
	}
	wg.Wait()
	if h := atomic.LoadInt32(&fu.hits); h != 1 {
		t.Fatalf("upstream hit %d times, want 1 (miss dedup failed)", h)
	}
}
