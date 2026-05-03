package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
	"github.com/allank/psst/internal/store"
)

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Bool("pretty", false, "human-readable output")
	statusCmd.Flags().String("store", "", "path to psst.db")
	statusCmd.Flags().String("format", "plain", "output format: plain, json")
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cache statistics",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, _ []string) error {
	pretty, _ := cmd.Flags().GetBool("pretty")
	format, _ := cmd.Flags().GetString("format")
	storeFlag, _ := cmd.Flags().GetString("store")

	cfg := loadConfig()
	storePath := resolveStorePath(storeFlag, cfg)

	s, err := openStore(storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
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
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	if fi, err := os.Stat(storePath); err == nil {
		info.SizeBytes = fi.Size()
	}

	if ok, uptime := checkDaemon(cfg.Server.Listen); ok {
		info.DaemonAddr = cfg.Server.Listen
		info.DaemonUptime = uptime
	}

	w := os.Stdout
	switch {
	case format == "json":
		output.WriteStatusJSON(w, info)
	case pretty:
		output.WriteStatusPretty(w, info)
	default:
		output.WriteStatus(w, info)
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
