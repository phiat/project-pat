package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"projectpat/internal/config"
	"projectpat/internal/db"
	"projectpat/internal/handlers"
	"projectpat/internal/llm"
	"projectpat/internal/scheduler"
	"projectpat/internal/store"
	"projectpat/internal/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[pat] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	st := store.New(database)
	client := llm.New(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, cfg.ModelFlash, cfg.ModelPro)
	renderer, err := web.NewRenderer(cfg.TemplatesDir)
	if err != nil {
		log.Fatalf("templates: %v", err)
	}
	h := handlers.New(st, client, renderer)

	mux := http.NewServeMux()
	h.Mount(mux)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))

	sc := scheduler.New(st, client)
	if err := sc.Start(); err != nil {
		log.Fatalf("scheduler: %v", err)
	}
	defer sc.Stop()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withLogging(mux),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		log.Printf("listening on http://localhost:%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	if err := os.MkdirAll(cfg.WorkspaceDir, 0o755); err != nil {
		log.Printf("workspace mkdir: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutdown initiated")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}
