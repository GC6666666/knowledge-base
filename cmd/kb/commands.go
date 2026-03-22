package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"kb/internal/core"
)

func newIngestCmd() *cobra.Command {
	var recursive, dryRun bool
	cmd := &cobra.Command{
		Use:   "ingest <path> [path...]",
		Short: "Ingest files or directories",
		Long:  "Ingest files into the knowledge base.\n\nSupported: jpg, jpeg, png, gif, webp, bmp, txt, md, pdf, doc, docx",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			p, err := resolvePipeline()
			if err != nil {
				return err
			}
		fmt.Fprintf(os.Stderr, "[DEBUG] Config: %+v\n", cfg)
			var processed, skipped, failed int
			for _, path := range args {
				info, err := os.Stat(path)
				if err != nil {
					log.Warn("path not found", "path", path, "err", err)
					failed++
					continue
				}
				if info.IsDir() {
					items, err := p.ProcessDirectory(ctx, path, recursive, log)
					if err != nil {
						log.Error("directory failed", "path", path, "err", err)
						failed++
						continue
					}
					processed += len(items)
				} else {
					if dryRun {
						log.Info("dry-run: would process", "path", path)
						skipped++
						continue
					}
					item, err := p.ProcessFile(ctx, path, log)
					if err != nil {
						log.Warn("file failed", "path", path, "err", err)
						failed++
						continue
					}
					processed++
					log.Info("done", "id", item.ID, "type", item.MediaType, "status", item.Status)
				}
			}
			fmt.Printf("\nIngest: %d processed, %d skipped, %d failed\n", processed, skipped, failed)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into subdirectories")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview without processing")
	return cmd
}

func newSearchCmd() *cobra.Command {
	var topK int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the knowledge base",
		Long:  "Search using semantic and full-text search.\nExample: kb search \"machine learning\"",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			p, err := resolvePipeline()
			if err != nil {
				return err
			}
			results, err := p.Search(ctx, args[0], topK)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			if len(results) == 0 {
				fmt.Println("No results.")
				return nil
			}
			if jsonOut {
				fmt.Printf(`{"query":%q,"count":%d,"results":[`, args[0], len(results))
				for i, r := range results {
					if i > 0 {
						fmt.Print(",")
					}
					if r.MediaItem == nil {
						continue
					}
					item := r.MediaItem
					chunk := r.ChunkText
					if len(chunk) > 200 {
						chunk = chunk[:200] + "..."
					}
					summary := ""
					if r.Summary != nil {
						summary = r.Summary.Summary
					}
					fmt.Printf("\n  {\"id\":%q,\"path\":%q,\"type\":%q,\"score\":%.4f,\"summary\":%q,\"chunk\":%q}",
						item.ID, item.SourcePath, item.MediaType, r.Score, summary, chunk)
				}
				fmt.Println("\n]}")
			} else {
				fmt.Printf("\nResults for: %s\n\n", args[0])
				for i, r := range results {
					if r.MediaItem == nil {
						continue
					}
					item := r.MediaItem
					shortPath := item.SourcePath
					if len(shortPath) > 55 {
						shortPath = "..." + shortPath[len(shortPath)-52:]
					}
					fmt.Printf("%d. [%s] %s (score: %.4f)\n", i+1, item.MediaType, shortPath, r.Score)
					if r.Summary != nil && r.Summary.Summary != "" {
						summary := r.Summary.Summary
						if len(summary) > 150 {
							summary = summary[:147] + "..."
						}
						fmt.Printf("   Summary: %s\n", summary)
					}
					if r.ChunkText != "" {
						chunk := r.ChunkText
						if len(chunk) > 200 {
							chunk = chunk[:197] + "..."
						}
						chunk = strings.ReplaceAll(chunk, "\n", " ")
						fmt.Printf("   %s\n", chunk)
					}
					fmt.Println()
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "topk", 10, "number of results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [item-id]",
		Short: "Show status of items",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			s, err := resolveStore()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				item, err := s.GetMediaItem(ctx, args[0])
				if err != nil {
					return err
				}
				if item == nil {
					fmt.Printf("Not found: %s\n", args[0])
					return nil
				}
				fmt.Printf("ID:      %s\n", item.ID)
				fmt.Printf("Path:    %s\n", item.SourcePath)
				fmt.Printf("Type:    %s\n", item.MediaType)
				fmt.Printf("Status:  %s\n", item.Status)
				fmt.Printf("Size:    %d bytes\n", item.FileSize)
				fmt.Printf("Hash:    %s\n", item.FileHash)
				if item.ErrorMsg != "" {
					fmt.Printf("Error:   %s\n", item.ErrorMsg)
				}
				fmt.Printf("Created: %s\n", item.CreatedAt)
				summary, _ := s.GetSummary(ctx, item.ID)
				if summary != nil {
					fmt.Printf("\nSummary: %s\n", summary.Summary)
					if len(summary.Tags) > 0 {
						fmt.Printf("Tags:    %s\n", strings.Join(summary.Tags, ", "))
					}
				}
				cls, _ := s.GetClassification(ctx, item.ID)
				if cls != nil {
					fmt.Printf("Topic:   %s (%.2f)\n", cls.Topic, cls.Confidence)
				}
			} else {
				items, total, err := s.ListMediaItems(ctx, "", "", 50, 0)
				if err != nil {
					return err
				}
				fmt.Printf("Total: %d items\n\n", total)
				for _, item := range items {
					icon := map[core.ItemStatus]string{
						core.StatusReady:      "●",
						core.StatusFailed:     "✗",
						core.StatusProcessing: "◐",
						core.StatusPending:    "○",
					}[item.Status]
					shortPath := item.SourcePath
					if len(shortPath) > 48 {
						shortPath = "..." + shortPath[len(shortPath)-45:]
					}
					fmt.Printf("%s [%s] %-8s %s\n", icon, item.ID[:8], item.MediaType, shortPath)
				}
			}
			return nil
		},
	}
	return cmd
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			s, err := resolveStore()
			if err != nil {
				return err
			}
			stats, err := s.Stats(ctx)
			if err != nil {
				return err
			}
			fmt.Println("\nKnowledge Base Stats")
			fmt.Println("====================")
			fmt.Printf("Total Items:  %d\n", stats["total_items"])
			fmt.Printf("Total Chunks: %d\n", stats["total_chunks"])
			fmt.Printf("Ready:        %d\n", stats["total_ready"])
			fmt.Println("\nBy Type:")
			for t, c := range stats["by_media_type"].(map[string]int) {
				fmt.Printf("  %-10s %d\n", t+":", c)
			}
			fmt.Println("\nBy Status:")
			for st, c := range stats["by_status"].(map[string]int) {
				fmt.Printf("  %-10s %d\n", st+":", c)
			}
			return nil
		},
	}
	return cmd
}
