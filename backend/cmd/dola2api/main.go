package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dola2api/internal/admin"
	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/gateway"
	"dola2api/internal/pool"
	"dola2api/internal/store"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DataDir, cfg.AdminUser, cfg.AdminPassword)
	if err != nil {
		log.Fatalf("初始化数据存储失败: %v", err)
	}

	settingsFn := func() config.Settings { return st.Settings() }
	client := dola.New(settingsFn)
	accountPool := pool.New(st, settingsFn)
	api := admin.New(st, accountPool, client, settingsFn)
	compat := gateway.New(st, accountPool, client, settingsFn)

	mux := http.NewServeMux()
	api.Register(mux)

	mux.HandleFunc("GET /health", compat.Health)
	mux.HandleFunc("GET /v1/models", compat.Models)
	mux.HandleFunc("POST /v1/chat/completions", compat.ChatCompletions)
	mux.HandleFunc("POST /v1/images/generations", compat.ImageGenerations)
	mux.HandleFunc("POST /v1/videos/generations", compat.VideoGenerations)

	mediaDir := st.Settings().Media.GeneratedDir
	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(mediaDir))))

	registerDebug(mux)
	registerStatic(mux, cfg.StaticDir)

	handler := withLogging(withCORS(mux))
	addr := st.Settings().Server.Addr
	if cfg.Addr != "" {
		addr = cfg.Addr
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go janitor(st)

	go func() {
		log.Printf("Dola2API %s 已启动 · 管理台 http://%s · 数据目录 %s", gateway.Version, displayAddr(addr), cfg.DataDir)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("关闭服务时出错: %v", err)
	}
	if err := st.Save(); err != nil {
		log.Printf("保存数据时出错: %v", err)
	}
	log.Println("已退出")
}

// registerDebug exposes pprof endpoints for diagnosing stalls. They are only
// served to loopback clients so a public deployment cannot leak heap contents.
func registerDebug(mux *http.ServeMux) {
	local := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// SplitHostPort understands bracketed IPv6 literals. Slicing at the
			// last colon instead leaves the brackets in place, so "[::1]:12345"
			// becomes "[::1]" and never matches the "::1" this list expects —
			// locking an IPv6 loopback client out of its own diagnostics.
			host := r.RemoteAddr
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if host != "127.0.0.1" && host != "::1" && host != "localhost" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /debug/pprof/", local(pprof.Index))
	mux.HandleFunc("GET /debug/pprof/goroutine", local(pprof.Handler("goroutine").ServeHTTP))
	mux.HandleFunc("GET /debug/pprof/heap", local(pprof.Handler("heap").ServeHTTP))
	mux.HandleFunc("GET /debug/pprof/mutex", local(pprof.Handler("mutex").ServeHTTP))
	mux.HandleFunc("GET /debug/pprof/block", local(pprof.Handler("block").ServeHTTP))
}

// registerStatic serves the built frontend and falls back to index.html so
// client-side routes work on refresh.
func registerStatic(mux *http.ServeMux, staticDir string) {
	index := filepath.Join(staticDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		log.Printf("提示: 未找到前端产物 %s，仅提供 API（可先执行 cd frontend && npm run build）", staticDir)
		return
	}

	fileServer := http.FileServer(http.Dir(staticDir))
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		// Never let the shell be cached: it names the hashed bundle, so a stale
		// copy keeps pointing at a JS file that no longer exists after a
		// rebuild. Without an explicit header Chrome falls back to heuristic
		// caching based on Last-Modified and can serve a stale index.html.
		w.Header().Set("cache-control", "no-cache")
		http.ServeFile(w, r, index)
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			serveIndex(w, r)
			return
		}
		// API prefixes are registered separately; anything else falls back to
		// the SPA entry point when the file does not exist.
		if _, err := os.Stat(filepath.Join(staticDir, filepath.Clean(strings.TrimPrefix(path, "/")))); err != nil {
			serveIndex(w, r)
			return
		}
		// Vite fingerprints asset filenames, so they can be cached forever.
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("cache-control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// janitor purges expired audits in the background.
func janitor(st *store.Store) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		st.PurgeAudits()
	}
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return addr
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("origin")
		if origin != "" {
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("access-control-allow-credentials", "true")
			w.Header().Set("access-control-allow-headers", "authorization,content-type,x-api-key,x-session-id")
			w.Header().Set("access-control-allow-methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/v1") || r.URL.Path == "/health" {
			started := time.Now()
			writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(writer, r)
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, writer.status, time.Since(started).Round(time.Millisecond))
			return
		}
		next.ServeHTTP(w, r)
	})
}
