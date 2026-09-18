package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// main.go is mostly wiring, but a few pieces carry logic worth pinning: the
// address displayed in the startup banner, the loopback gate in front of the
// pprof endpoints, and the two middlewares.

func TestDisplayAddrRewritesWildcardToLoopback(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0.0.0.0:8080", "127.0.0.1:8080"},
		{"0.0.0.0:18080", "127.0.0.1:18080"},
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{":8080", ":8080"},
		{"[::]:8080", "[::]:8080"},
		{"example.test:443", "example.test:443"},
		{"0.0.0.0:", "127.0.0.1:"},
	}

	for _, tc := range cases {
		if got := displayAddr(tc.in); got != tc.want {
			t.Errorf("displayAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The pprof handlers expose heap and goroutine dumps, so they must never answer
// a non-loopback client. The gate parses RemoteAddr by hand, which is exactly
// the kind of code that mishandles bracketed IPv6 addresses.
func TestDebugEndpointsAreLoopbackOnly(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		wantStatus int
	}{
		{"ipv4 loopback with port", "127.0.0.1:12345", http.StatusOK},
		{"ipv4 loopback without port", "127.0.0.1", http.StatusOK},
		{"ipv6 loopback with port", "[::1]:12345", http.StatusOK},
		{"ipv6 loopback without port", "::1", http.StatusOK},
		{"public ipv4", "203.0.113.7:12345", http.StatusNotFound},
		{"private ipv4 is still not loopback", "192.168.1.10:12345", http.StatusNotFound},
		{"public ipv6", "[2001:db8::1]:12345", http.StatusNotFound},
		{"empty remote addr", "", http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerDebug(mux)

			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			req.RemoteAddr = tc.remoteAddr
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("RemoteAddr=%q got status %d, want %d", tc.remoteAddr, rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestDebugRegistersTheExpectedProfiles(t *testing.T) {
	mux := http.NewServeMux()
	registerDebug(mux)

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/goroutine",
		"/debug/pprof/heap",
		"/debug/pprof/mutex",
		"/debug/pprof/block",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d for a loopback client, want 200", path, rec.Code)
		}
	}
}

func TestCORSEchoesOriginWhenPresent(t *testing.T) {
	var reached bool
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("origin", "https://console.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("the wrapped handler was never called")
	}
	if got := rec.Header().Get("access-control-allow-origin"); got != "https://console.example" {
		t.Errorf("allow-origin = %q, want the request origin echoed back", got)
	}
	if got := rec.Header().Get("access-control-allow-credentials"); got != "true" {
		t.Errorf("allow-credentials = %q", got)
	}
	// The console sends its session token in this header, so it has to be listed
	// or the browser blocks the preflight.
	if got := rec.Header().Get("access-control-allow-headers"); got == "" {
		t.Error("allow-headers is empty; the console's auth header would be blocked")
	}
}

func TestCORSSkipsHeadersWithoutOrigin(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("access-control-allow-origin"); got != "" {
		t.Errorf("allow-origin = %q, want no CORS headers for a same-origin request", got)
	}
}

func TestCORSShortCircuitsPreflight(t *testing.T) {
	var reached bool
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("origin", "https://console.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if reached {
		t.Error("an OPTIONS preflight reached the wrapped handler instead of being answered directly")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
}

// statusWriter supplies the default 200 for handlers that never call WriteHeader
// (the SSE endpoints in particular), so the access log does not report 0.
func TestStatusWriterDefaultsToOK(t *testing.T) {
	rec := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	if _, err := writer.Write([]byte("chunk")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if writer.status != http.StatusOK {
		t.Errorf("status = %d, want 200 for a handler that never set one", writer.status)
	}
}

func TestStatusWriterRecordsExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	writer.WriteHeader(http.StatusTeapot)

	if writer.status != http.StatusTeapot {
		t.Errorf("status = %d, want 418", writer.status)
	}
}

func TestStaticServesIndexWithNoCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>shell</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	mux := http.NewServeMux()
	registerStatic(mux, dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	// A cached shell keeps naming a hashed bundle that no longer exists after a
	// rebuild, so the entry point must always be revalidated.
	if got := rec.Header().Get("cache-control"); got != "no-cache" {
		t.Errorf("cache-control = %q, want no-cache on the SPA shell", got)
	}
}

func TestStaticFallsBackToIndexForUnknownRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>shell</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	mux := http.NewServeMux()
	registerStatic(mux, dir)

	// A client-side route: no such file on disk, so the shell must be served or
	// a refresh on that page would 404.
	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts = %d, want the SPA fallback to answer 200", rec.Code)
	}
	if body := rec.Body.String(); body != "<html>shell</html>" {
		t.Errorf("body = %q, want the shell", body)
	}
}

func TestStaticMarksAssetsImmutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>shell</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-abc123.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	mux := http.NewServeMux()
	registerStatic(mux, dir)

	req := httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET asset = %d, want 200", rec.Code)
	}
	// Vite fingerprints asset names, so content changes rename the file.
	if got := rec.Header().Get("cache-control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("cache-control = %q, want the immutable directive", got)
	}
}

func TestStaticWithoutBuildServesNothing(t *testing.T) {
	mux := http.NewServeMux()
	registerStatic(mux, filepath.Join(t.TempDir(), "missing"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Without a build there is no SPA to serve; the API-only mode must not
	// invent a 200 for the console.
	if rec.Code == http.StatusOK {
		t.Error("GET / returned 200 with no frontend build present")
	}
}
