package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	docsDir := envOr("DOCS_DIR", "docs")
	addr := envOr("ADDR", ":8080")
	upstream := envOr("UPSTREAM", "https://mise-versions.jdx.dev")
	cacheDir := envOr("CACHE_DIR", "cache")
	ttl := envDuration("CACHE_TTL", time.Hour)

	proxy := newMirror(upstream, cacheDir, ttl)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		log.Fatalf("cannot create cache dir %s: %v", cacheDir, err)
	}

	fileServer := http.FileServer(http.Dir(docsDir))

	mux := http.NewServeMux()
	// mise requests /data/<tool>.toml; docs/<tool>.toml holds the same bytes.
	mux.Handle("/data/", tomlContentType(http.StripPrefix("/data/", fileServer)))
	// core:python fetches /tools/<name>.gz (a gzipped asset list); serve it
	// reconstructed from docs/<name>.toml. A 404 here is fatal (no fallback).
	mux.Handle("/tools/", toolsGzHandler(docsDir))
	// GitHub release mirror. Proxy to the real host, record each response into a
	// path-mirrored tree, and fall back to the (possibly stale) copy when the
	// upstream is unreachable. This keeps mise off api.github.com, which
	// rate-limits unauthenticated callers.
	mux.Handle("/api/github/", proxy)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("serving %s at %s (GET /data/<tool>.toml, /tools/<name>.gz; /api/github/* -> %s mirrored in %s, ttl %s)",
		docsDir, addr, upstream, cacheDir, ttl)
	log.Fatal(http.ListenAndServe(addr, logRequests(mux)))
}

// statusRecorder captures the response status code so the request log can
// distinguish served version lists (200) from unhandled routes (404).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests logs every incoming request with method, path, status, and duration.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Default to 200: FileServer serves successful responses without
		// ever calling WriteHeader explicitly.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		cache := ""
		if x := rec.Header().Get("X-Cache"); x != "" {
			cache = " [" + x + "]"
		}
		log.Printf("%s %s %s -> %d%s (%s)", r.RemoteAddr, r.Method, r.URL.Path, rec.status, cache, time.Since(start))
	})
}

// toolsGzHandler serves /tools/<name>.gz, which mise (core:python) fetches as a
// gzipped, newline-separated list of precompiled cpython asset names. Upstream
// generates that plain-text list then deletes it, committing only the derived
// docs/<name>.toml — where each line is exactly the quoted key of the [versions]
// table. So reconstruct the list from that toml and gzip it on the fly (matches
// mise-versions.jdx.dev byte-for-byte). Unlike /data or /api/github, a 404 here
// aborts the python install, so this route must succeed.
func toolsGzHandler(docsDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tools/")
		base, ok := strings.CutSuffix(name, ".gz")
		if !ok || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(docsDir, base+".toml"))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()

		// Emit each [versions] key, one per line. Restricting to that table
		// guards against any future non-version quoted keys elsewhere.
		inVersions := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "[") {
				inVersions = line == "[versions]"
				continue
			}
			if !inVersions {
				continue
			}
			if key, found := strings.CutPrefix(line, `"`); found {
				if i := strings.IndexByte(key, '"'); i >= 0 {
					gz.Write([]byte(key[:i] + "\n"))
				}
			}
		}
	})
}

// mirror is a disk-caching reverse proxy for the /api/github/* release mirror. It
// forwards to a real mise-versions host and records each 2xx response at a disk
// path that mirrors the request path (api/github/repos/.../latest), with a
// "<path>.meta" sidecar for the headers. That layout is servable as-is by a dumb
// static host (e.g. GitHub Pages) — the request path IS the file path — while the
// live server revalidates once an entry goes stale (via ETag / Last-Modified when
// offered, else a TTL) and replays the real Content-Type from .meta. The tree is
// self-contained and path-portable: stop the server, copy or commit the cache
// dir, and it is reused as-is. If the upstream is unreachable but a copy exists
// (even a stale one), that copy is served.
type mirror struct {
	upstream string
	cacheDir string
	ttl      time.Duration
	client   *http.Client
}

func newMirror(upstream, cacheDir string, ttl time.Duration) *mirror {
	return &mirror{
		upstream: strings.TrimRight(upstream, "/"),
		cacheDir: cacheDir,
		ttl:      ttl,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// cacheMeta is the sidecar record stored next to each cached body. It carries
// only relative/portable data (no absolute paths), so the cache dir can be
// copied or committed and reused unchanged.
type cacheMeta struct {
	URL          string `json:"url"`
	Status       int    `json:"status"`
	ContentType  string `json:"content_type"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	FetchedAt    int64  `json:"fetched_at"` // unix seconds of the last (re)validation
	MaxAge       int64  `json:"max_age"`    // freshness window in seconds (0 => use TTL)
}

// mirrorPaths maps a request path to its on-disk body and meta paths under the
// cache dir. The query string is ignored (a static host ignores it too, and the
// mirrored leaf endpoints carry none). ok is false for paths that map to the
// cache root or would escape it via "..".
func mirrorPaths(cacheDir, reqPath string) (bodyPath, metaPath string, ok bool) {
	// Leading slash + Clean resolves any ".." so the result cannot climb above
	// root; an empty or root-only path has nothing to cache.
	clean := path.Clean("/" + strings.TrimPrefix(reqPath, "/"))
	if clean == "/" {
		return "", "", false
	}
	bodyPath = filepath.Join(cacheDir, filepath.FromSlash(clean))
	// Defense in depth: confirm the join stayed inside the cache dir. rel handles
	// a relative cacheDir (e.g. "." for the repo-root loop) that a string-prefix
	// check would mishandle.
	rel, err := filepath.Rel(cacheDir, bodyPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", false
	}
	return bodyPath, bodyPath + ".meta", true
}

func (p *mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyPath, metaPath, ok := mirrorPaths(p.cacheDir, r.URL.Path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	meta, body, cached := loadCache(metaPath, bodyPath)

	// Fresh cache: serve straight from disk, no network at all.
	if cached && p.fresh(meta) {
		p.serve(w, r, meta, body, "HIT")
		return
	}

	// Stale or missing: ask the upstream, revalidating when we have an entry.
	resp, err := p.fetch(r.URL, cached, meta)
	if err != nil {
		if cached { // upstream unreachable but we have a copy — serve it.
			p.serve(w, r, meta, body, "STALE")
			return
		}
		http.Error(w, "upstream unreachable and no cached copy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && cached:
		// Unchanged upstream; refresh freshness bookkeeping and serve cache.
		meta.FetchedAt = time.Now().Unix()
		meta.MaxAge = maxAgeSeconds(resp.Header.Get("Cache-Control"))
		if etag := resp.Header.Get("ETag"); etag != "" {
			meta.ETag = etag
		}
		_ = writeMeta(metaPath, meta)
		p.serve(w, r, meta, body, "REVALIDATED")

	case resp.StatusCode == http.StatusOK:
		fresh, err := io.ReadAll(resp.Body)
		if err != nil {
			if cached {
				p.serve(w, r, meta, body, "STALE")
				return
			}
			http.Error(w, "reading upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		newMeta := cacheMeta{
			URL:          p.upstream + r.URL.Path,
			Status:       http.StatusOK,
			ContentType:  resp.Header.Get("Content-Type"),
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			FetchedAt:    time.Now().Unix(),
			MaxAge:       maxAgeSeconds(resp.Header.Get("Cache-Control")),
		}
		if err := p.write(bodyPath, metaPath, newMeta, fresh); err != nil {
			log.Printf("cache write failed for %s: %v", r.URL.Path, err)
		}
		p.serve(w, r, newMeta, fresh, "MISS")

	default:
		// Non-cacheable upstream status (e.g. 404 for an unmirrored repo).
		// Pass it through untouched so mise can fall back to api.github.com.
		w.Header().Set("X-Cache", "BYPASS")
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		if r.Method != http.MethodHead {
			io.Copy(w, resp.Body)
		}
	}
}

// write records a body+meta into the mirrored tree, creating parent dirs first.
// It refuses to clobber a path already taken by a directory (which would only
// happen if mise requested both a path and a descendant of it — the release list
// endpoint that would cause this 404s upstream, so it should not occur).
func (p *mirror) write(bodyPath, metaPath string, m cacheMeta, body []byte) error {
	if info, err := os.Stat(bodyPath); err == nil && info.IsDir() {
		return fmt.Errorf("path %s already exists as a directory", bodyPath)
	}
	if err := os.MkdirAll(filepath.Dir(bodyPath), 0o755); err != nil {
		return err
	}
	return writeCache(metaPath, bodyPath, m, body)
}

// fresh reports whether a cached entry is still within its freshness window.
func (p *mirror) fresh(m cacheMeta) bool {
	ttl := time.Duration(m.MaxAge) * time.Second
	if ttl <= 0 {
		ttl = p.ttl
	}
	return time.Since(time.Unix(m.FetchedAt, 0)) < ttl
}

// fetch requests the upstream mirror, attaching conditional-request headers when
// a cached entry exists so the upstream can answer 304 Not Modified.
func (p *mirror) fetch(u *url.URL, cached bool, m cacheMeta) (*http.Response, error) {
	target := p.upstream + u.Path
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if cached {
		if m.ETag != "" {
			req.Header.Set("If-None-Match", m.ETag)
		}
		if m.LastModified != "" {
			req.Header.Set("If-Modified-Since", m.LastModified)
		}
	}
	return p.client.Do(req)
}

// serve writes a cached entry to the client, tagging it with the cache
// disposition for the request log.
func (p *mirror) serve(w http.ResponseWriter, r *http.Request, m cacheMeta, body []byte, disposition string) {
	w.Header().Set("X-Cache", disposition)
	if m.ContentType != "" {
		w.Header().Set("Content-Type", m.ContentType)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(m.Status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

func loadCache(metaPath, bodyPath string) (cacheMeta, []byte, bool) {
	var m cacheMeta
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return m, nil, false
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, nil, false
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return m, nil, false
	}
	return m, body, true
}

// writeCache persists a body then its meta, each via a temp file + atomic
// rename. Meta is written last, so a reader that sees a .meta always finds a
// complete body alongside it.
func writeCache(metaPath, bodyPath string, m cacheMeta, body []byte) error {
	if err := atomicWrite(bodyPath, body); err != nil {
		return err
	}
	return writeMeta(metaPath, m)
}

func writeMeta(metaPath string, m cacheMeta) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(metaPath, raw)
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// maxAgeSeconds pulls the max-age value out of a Cache-Control header, or 0.
func maxAgeSeconds(cacheControl string) int64 {
	for _, part := range strings.Split(cacheControl, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "max-age="); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func tomlContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/toml; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("invalid %s=%q, using default %s", key, v, def)
	}
	return def
}
