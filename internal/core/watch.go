package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors directories for new/changed files and ingests them.
type Watcher struct {
	watcher   *fsnotify.Watcher
	pipeline *Pipeline
	config   *WatchConfig
	ingestCfg *IngestConfig
	log      *slog.Logger

	// pending tracks files waiting to be processed (debounce)
	pending    map[string]time.Time
	stopChan   chan struct{}
	processAll bool
}

// NewWatcher creates a new directory watcher.
func NewWatcher(pipeline *Pipeline, watchCfg *WatchConfig, ingestCfg *IngestConfig, log *slog.Logger) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	var paths []string
	for _, p := range watchCfg.Paths {
		if p != "" {
			abs, err := filepath.Abs(p)
			if err == nil {
				paths = append(paths, abs)
			}
		}
	}

	return &Watcher{
		watcher:   w,
		pipeline: pipeline,
		config:   watchCfg,
		ingestCfg: ingestCfg,
		log:      log,
		pending:  make(map[string]time.Time),
		stopChan: make(chan struct{}),
	}, nil
}

// Start begins watching all configured paths.
func (w *Watcher) Start(ctx context.Context) error {
	for _, p := range w.config.Paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			w.log.Warn("invalid watch path", "path", p, "err", err)
			continue
		}
		if err := w.watcher.Add(abs); err != nil {
			return fmt.Errorf("add watch path %s: %w", abs, err)
		}
		w.log.Info("watching", "path", abs)
	}

	if len(w.config.Paths) == 0 {
		return fmt.Errorf("no paths configured for watching")
	}

	// Determine debounce duration
	debounce := 2 * time.Second
	if w.config.Debounce != "" {
		if d, err := time.ParseDuration(w.config.Debounce); err == nil {
			debounce = d
		}
	}

	go w.run(ctx, debounce)
	return nil
}

func (w *Watcher) run(ctx context.Context, debounce time.Duration) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				w.queueFile(event.Name)
			}
		case err := <-w.watcher.Errors:
			if err != nil {
				w.log.Warn("watcher error", "err", err)
			}
		case <-ticker.C:
			w.processPending(ctx)
		}
	}
}

func (w *Watcher) queueFile(path string) {
	// Skip directories
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		// If it's a new directory, watch it
		w.watcher.Add(path)
		return
	}

	// Check if supported type
	ext := filepath.Ext(path)
	if !w.ingestCfg.IsSupportedType(ext) {
		return
	}

	w.pending[path] = time.Now()
}

func (w *Watcher) processPending(ctx context.Context) {
	now := time.Now()
	debounce := 2 * time.Second
	if w.config.Debounce != "" {
		if d, err := time.ParseDuration(w.config.Debounce); err == nil {
			debounce = d
		}
	}

	for path, queued := range w.pending {
		if now.Sub(queued) >= debounce {
			delete(w.pending, path)

			// Check file still exists
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}

			w.log.Info("auto-ingesting", "path", path)
			item, err := w.pipeline.ProcessFile(ctx, path, w.log)
			if err != nil {
				w.log.Warn("auto-ingest failed", "path", path, "err", err)
				continue
			}
			w.log.Info("auto-ingested", "id", item.ID, "type", item.MediaType)
		}
	}
}

// Stop halts the watcher.
func (w *Watcher) Stop() error {
	close(w.stopChan)
	return w.watcher.Close()
}

// Watch starts watching paths and returns immediately.
// Returns a channel that blocks until Stop is called.
func (w *Watcher) Watch(ctx context.Context) error {
	return w.Start(ctx)
}

// EnsureSupportedExt checks if a file extension is supported by ingest config.
func (w *Watcher) EnsureSupportedExt(ext string) bool {
	return w.ingestCfg.IsSupportedType(ext)
}

// FileFilter returns true if the file should be ingested.
func FileFilter(path string, cfg *IngestConfig) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	ext := filepath.Ext(path)
	return cfg.IsSupportedType(ext)
}

// NormalizePath converts a path to absolute form, handling ~ and relative paths.
func NormalizePath(p string) (string, error) {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}
