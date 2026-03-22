package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"kb/internal/core"
)

var (
	configPath string
	verbose   bool
)

var (
	cfg      *core.Config
	store    *core.Store
	pipeline *core.Pipeline
	log      *slog.Logger
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kb",
		Short: "kb - Knowledge Base",
		Long:  "Knowledge base with AI summarization, classification, and semantic search.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "config file (default: ./kb.yaml)")
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	cmd.AddCommand(newIngestCmd())
	cmd.AddCommand(newSearchCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newStatsCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newWatchCmd())
	cmd.AddCommand(newDBCmd())
	return cmd
}

func initConfig() error {
	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "time", Value: slog.StringValue(a.Value.Any().(time.Time).Format("15:04:05"))}
			}
			return a
		},
	}))

	var err error
	cfg, err = core.LoadConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = &core.Config{}
		} else {
			return fmt.Errorf("load config: %w", err)
		}
	}
	return nil
}

func resolveStore() (*core.Store, error) {
	if store != nil {
		return store, nil
	}
	ctx := context.Background()
	dsn := cfg.Database.DSN()
	s, err := core.NewStore(ctx, dsn, cfg.Database.MaxConns, cfg.Database.MinConns)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w (run 'kb db init' first)", err)
	}
	store = s
	return store, nil
}

func resolvePipeline() (*core.Pipeline, error) {
	if pipeline != nil {
		return pipeline, nil
	}
	s, err := resolveStore()
	if err != nil {
		return nil, err
	}
	var ai core.AIProvider
	provider := strings.ToLower(cfg.AI.Provider)
	switch provider {
	case "", "minimax":
		if cfg.AI.Minimax.APIKey != "" {
			ai = core.NewMinimaxProvider(
				cfg.AI.Minimax.APIKey,
				cfg.AI.Minimax.BaseURL,
				cfg.AI.Minimax.GroupID,
				cfg.AI.Minimax.Model,
				cfg.AI.Minimax.EmbeddingModel,
				cfg.AI.Minimax.EmbeddingDim,
				cfg.AI.Minimax.MaxTokens,
				cfg.AI.Minimax.Temperature,
			)
		}
	case "codex", "codex-for.me", "codexforme":
		if cfg.AI.Codex.APIKey != "" {
			ai = core.NewCodexProvider(
				cfg.AI.Codex.APIKey,
				cfg.AI.Codex.BaseURL,
				cfg.AI.Codex.Model,
				cfg.AI.Codex.EmbeddingModel,
				cfg.AI.Codex.EmbeddingDim,
				2048,
				0.7,
				cfg.AI.Codex.ReasoningEffort,
			)
		}
	default:
		// Unknown provider: leave ai nil; pipeline will skip AI work.
	}
	pipeline = core.NewPipeline(s, ai, cfg)
	return pipeline, nil
}

// ensureDir ensures the data directory exists
func ensureDataDir() {
	if cfg.App.DataDir == "" {
		return
	}
	abs, _ := filepath.Abs(cfg.App.DataDir)
	os.MkdirAll(abs, 0755)
}
