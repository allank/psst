package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
	"github.com/allank/psst/internal/output"
	"github.com/allank/psst/internal/store"
)

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Bool("pretty", false, "human-readable output")
	statusCmd.Flags().String("store", "", "path to psst.db")
	statusCmd.Flags().String("format", "plain", "output format: plain, json")

	murlicobra.Annotate(statusCmd, murli.Metadata{
		AgentDescription: "Retrieves stats and health information of the semantic cache database (e.g. database path, entries, expired keys, store size, tool counts, daemon address, and uptime).",
		WhenToUse:        "Use when you want to inspect how many items are currently cached in the system, check cache storage size, or verify daemon process connectivity and health status.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "Cache health and usage statistics",
			Shape: map[string]any{
				"store":         "string (absolute path to psst.db)",
				"entries":       "int (total count of stored entries)",
				"expired":       "int (count of expired entries pending eviction)",
				"size_bytes":    "int (size of database file in bytes)",
				"tool_counts":   "object (mapping of tool name to cached count)",
				"daemon_addr":   "string (IP:Port of daemon if running)",
				"daemon_uptime": "string (uptime duration description)",
			},
		},
		Examples: []string{
			"psst status",
			"psst status --pretty",
		},
	})
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cache statistics",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, _ []string) error {
	pretty, _ := cmd.Flags().GetBool("pretty")
	storeFlag, _ := cmd.Flags().GetString("store")

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
	defer s.Close()

	info := &output.StatusInfo{
		StorePath:  storePath,
		ToolCounts: make(map[string]int),
	}
	now := time.Now()
	if err := s.Scan(func(e *store.Entry) error {
		info.Entries++
		if now.After(e.ExpiresAt) {
			info.Expired++
		}
		info.ToolCounts[e.Tool]++
		return nil
	}); err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "database_scan_error",
			Message:     fmt.Sprintf("failed to scan store: %v", err),
			Recoverable: false,
		}
	}

	if fi, err := os.Stat(storePath); err == nil {
		info.SizeBytes = fi.Size()
	}

	if ok, uptime := checkDaemon(cfg.Server.Listen); ok {
		info.DaemonAddr = cfg.Server.Listen
		info.DaemonUptime = uptime
	}

	writer := murlicobra.NewWriter(cmd)
	w := os.Stdout
	if writer.IsTTY() {
		if pretty {
			output.WriteStatusPretty(w, info)
		} else {
			output.WriteStatus(w, info)
		}
	} else {
		output.WriteStatusJSON(w, info)
	}
	return nil
}

func checkDaemon(addr string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return false, ""
	}
	defer resp.Body.Close()
	var body struct {
		Uptime string `json:"uptime"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return true, body.Uptime
}
