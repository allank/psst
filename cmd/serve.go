package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
	"github.com/allank/psst/internal/mcpserver"
	"github.com/allank/psst/internal/store"
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().String("listen", "127.0.0.1:7425", "MCP server bind address")
	serveCmd.Flags().String("store", "", "path to psst.db")
	serveCmd.Flags().Bool("pretty", false, "human-readable log output")

	murlicobra.Annotate(serveCmd, murli.Metadata{
		AgentDescription: "Starts the persistent semantic cache Model Context Protocol (MCP) server over HTTP.",
		WhenToUse:        "Use when you want to start the daemon process to persistently handle and cache tool queries from AI clients.",
		Idempotent:       false,
		Returns: &murli.ReturnSchema{
			Type:        "text",
			Description: "Persistent execution log",
		},
		Examples: []string{
			"psst serve --listen 127.0.0.1:7425",
		},
	})
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP HTTP daemon",
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, _ []string) error {
	listenAddr, _ := cmd.Flags().GetString("listen")
	storeFlag, _ := cmd.Flags().GetString("store")
	pretty, _ := cmd.Flags().GetBool("pretty")

	if pretty {
		log.SetLevel(log.DebugLevel)
	}

	cfg := loadConfig()
	storePath := resolveStorePath(storeFlag, cfg)

	s, err := openStore(storePath)
	if err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "store_error",
			Message:     fmt.Sprintf("failed to open database: %v", err),
			Recoverable: false,
		}
	}

	emb, _ := loadEmbedder()
	if emb != nil {
		defer emb.Close()
	}

	c := openCache(s, cfg, emb, storePath)
	c.EnsureIndex()

	sweepInterval := 10 * time.Minute
	if cfg != nil && cfg.Eviction.SweepIntervalMinutes > 0 {
		sweepInterval = time.Duration(cfg.Eviction.SweepIntervalMinutes) * time.Minute
	}
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			if n, err := c.Evict(); err == nil && n > 0 {
				log.Info("evicted expired entries", "count", n)
			}
		}
	}()

	entryFunc := func() int {
		var n int
		_ = s.Scan(func(_ *store.Entry) error { n++; return nil })
		return n
	}

	srv := mcpserver.New(listenAddr, c, entryFunc, storePath)
	if err := srv.Start(); err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "bind_failed",
			Message:     fmt.Sprintf("failed to bind server to address %s: %v", listenAddr, err),
			Recoverable: false,
		}
	}

	log.Info("serving", "store", storePath, "listen", srv.Addr(), "entries", entryFunc())

	sigCh := make(chan os.Signal, 1)
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	signal.Notify(sighupCh, syscall.SIGHUP)

	for {
		select {
		case <-sigCh:
			log.Info("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			_ = c.SaveSidecar()
			s.Close()
			return nil

		case <-sighupCh:
			log.Info("rebuilding index on SIGHUP")
			if err := c.RebuildIndex(); err != nil {
				log.Error("rebuild failed", "err", err)
			} else {
				log.Info("index rebuilt", "entries", entryFunc())
			}
		}
	}
}
