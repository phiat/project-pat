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
	client, err := llm.New(llm.Opts{
		Provider:       cfg.LLMProvider,
		APIKey:         cfg.LLMAPIKey,
		BaseURL:        cfg.LLMBaseURL,
		ModelFlash:     cfg.LLMModelFlash,
		ModelPro:       cfg.LLMModelPro,
		ReasoningFlash: cfg.LLMReasoningFlash,
		ReasoningPro:   cfg.LLMReasoningPro,
	})
	if err != nil {
		log.Fatalf("llm: %v", err)
	}
	log.Printf("llm: provider=%s  flash=%s (reasoning=%s)  pro=%s (reasoning=%s)",
		client.Provider(),
		cfg.LLMModelFlash, cfg.LLMReasoningFlash,
		cfg.LLMModelPro, cfg.LLMReasoningPro,
	)
	renderer, err := web.NewRenderer(cfg.TemplatesDir)
	if err != nil {
		log.Fatalf("templates: %v", err)
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	sc := scheduler.New(st, client)
	sc.RootCtx = rootCtx

	h := handlers.New(st, client, renderer)
	h.RootCtx = rootCtx
	h.OnAgentChanged = sc.Reload

	mux := http.NewServeMux()
	h.Mount(mux)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))

	if err := sc.Start(); err != nil {
		log.Fatalf("scheduler: %v", err)
	}
	defer sc.Stop()

	addr := cfg.Host + ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           withLogging(mux),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		shown := cfg.Host
		if shown == "0.0.0.0" || shown == "" {
			shown = "0.0.0.0 (all interfaces) — also reachable at http://localhost:" + cfg.Port + " and on the LAN"
		}
		log.Printf("listening on %s:%s · %s", cfg.Host, cfg.Port, shown)
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
	// signal in-flight agent goroutines to abort, then close http
	rootCancel()
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
