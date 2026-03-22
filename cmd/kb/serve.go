package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"kb/internal/core"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start HTTP API server",
		Long:  "Start the knowledge base HTTP API server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			host := cfg.App.Host
			if host == "" {
				host = "0.0.0.0"
			}
			port := cfg.App.Port
			if port == 0 {
				port = 8088
			}
			addr := fmt.Sprintf("%s:%d", host, port)

			http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"status":"ok"}`))
			})
			http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" || r.URL.Path == "/index.html" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write([]byte(WebUI))
					return
				}
				http.NotFound(w, r)
			})
			http.HandleFunc("/api/media", handleMedia)
			http.HandleFunc("/api/search", handleSearch)
			http.HandleFunc("/api/stats", handleStats)

			fmt.Printf("KB server starting on %s\n", addr)
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				if err := http.ListenAndServe(addr, nil); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
				}
			}()
			<-sigChan
			fmt.Println("Shutting down...")
			return nil
		},
	}
	return cmd
}

func handleMedia(w http.ResponseWriter, r *http.Request) {
	s, err := resolveStore()
	if err != nil {
		http.Error(w, fmt.Sprintf("store error: %v", err), 500)
		return
	}
	ctx := context.Background()

	if r.Method == "GET" {
		mt := r.URL.Query().Get("type")
		st := r.URL.Query().Get("status")
		limit := 20
		offset := 0
		if l, _ := strconv.Atoi(r.URL.Query().Get("limit")); l > 0 {
			limit = l
		}
		if o, _ := strconv.Atoi(r.URL.Query().Get("offset")); o > 0 {
			offset = o
		}
		items, total, err := s.ListMediaItems(ctx, mt, st, limit, offset)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, `{"total":%d,"items":[`, total)
		for i, item := range items {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%q,"path":%q,"type":%q,"status":%q,"created":%q}`,
				item.ID, item.SourcePath, item.MediaType, item.Status, item.CreatedAt)
		}
		fmt.Fprint(w, "]}")
	} else {
		http.Error(w, "use CLI: kb ingest <path>", 400)
	}
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePipeline()
	if err != nil {
		http.Error(w, fmt.Sprintf("pipeline error: %v", err), 500)
		return
	}
	ctx := context.Background()
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing ?q=", 400)
		return
	}
	topK := 10
	if k, _ := strconv.Atoi(r.URL.Query().Get("topk")); k > 0 {
		topK = k
	}
	results, err := p.Search(ctx, query, topK)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fmt.Fprintf(w, `{"query":%q,"count":%d,"results":[`, query, len(results))
	for i, r := range results {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		if r.MediaItem != nil {
			chunk := r.ChunkText
			if len(chunk) > 200 {
				chunk = chunk[:200] + "..."
			}
			fmt.Fprintf(w, `{"id":%q,"path":%q,"type":%q,"score":%.4f,"chunk":%q}`,
				r.MediaItem.ID, r.MediaItem.SourcePath, r.MediaItem.MediaType, r.Score, chunk)
		}
	}
	fmt.Fprint(w, "]}")
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	s, err := resolveStore()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ctx := context.Background()
	stats, err := s.Stats(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fmt.Fprintf(w, `{"items":%d,"chunks":%d,"ready":%d}`,
		stats["total_items"], stats["total_chunks"], stats["total_ready"])
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch [path...]",
		Short: "Watch directories for changes",
		Long: "Watch directories and auto-ingest new files.\nExamples:\n  kb watch ./documents/\n  kb watch ./data/ ./uploads/",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			p, err := resolvePipeline()
			if err != nil {
				return err
			}

			paths := args
			if len(paths) == 0 {
				paths = cfg.Watch.Paths
			}
			if len(paths) == 0 {
				return fmt.Errorf("no paths to watch (specify as args or in config.watch.paths)")
			}

			watchPaths := make([]string, 0, len(paths))
			for _, p := range paths {
				abs, err := core.NormalizePath(p)
				if err != nil {
					return fmt.Errorf("invalid path %s: %w", p, err)
				}
				watchPaths = append(watchPaths, abs)
			}

			watchCfg := &core.WatchConfig{
				Enabled:  true,
				Paths:    watchPaths,
				Debounce: cfg.Watch.Debounce,
			}

			watcher, err := core.NewWatcher(p, watchCfg, &cfg.Ingest, log)
			if err != nil {
				return fmt.Errorf("create watcher: %w", err)
			}

			fmt.Println("KB Watcher")
			fmt.Println("==========")
			for _, p := range watchPaths {
				fmt.Printf("  watching: %s\n", p)
			}
			fmt.Println("\nCtrl+C to stop.\n")

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			go func() {
				if err := watcher.Watch(ctx); err != nil {
					log.Error("watcher error", "err", err)
				}
			}()

			<-sigChan
			fmt.Println("\nStopping...")
			watcher.Stop()
			return nil
		},
	}
}

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database management",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize and verify database connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			fmt.Printf("Connecting to %s:%d...\n", cfg.Database.Host, cfg.Database.Port)
			s, err := core.NewStore(ctx, cfg.Database.DSN(), cfg.Database.MaxConns, cfg.Database.MinConns)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed: %v\n", err)
				fmt.Fprintln(os.Stderr, "\nStart PostgreSQL with pgvector:")
				fmt.Fprintln(os.Stderr, "  docker compose up -d")
				return err
			}
			defer s.Close()
			fmt.Println("Connected successfully!")
			fmt.Println("Schema: media_items, summaries, classifications, text_chunks, embeddings")
			return nil
		},
	})
	return cmd
}
