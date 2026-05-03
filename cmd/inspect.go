package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
	"github.com/allank/psst/internal/store"
)

func init() {
	rootCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().String("tool", "", "filter by tool name")
	inspectCmd.Flags().String("key", "", "show single entry by key")
	inspectCmd.Flags().Bool("expired", false, "include expired entries")
	inspectCmd.Flags().Bool("pretty", false, "human-readable table output")
	inspectCmd.Flags().String("format", "plain", "output format: plain, json")
	inspectCmd.Flags().String("store", "", "path to psst.db")
}

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Show stored entries",
	RunE:  runInspect,
}

func runInspect(cmd *cobra.Command, _ []string) error {
	toolFilter, _ := cmd.Flags().GetString("tool")
	keyFilter, _ := cmd.Flags().GetString("key")
	includeExpired, _ := cmd.Flags().GetBool("expired")
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

	var entries []*store.Entry
	now := time.Now()

	if keyFilter != "" {
		e, err := s.Get(keyFilter)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		if e != nil {
			entries = append(entries, e)
		}
	} else {
		_ = s.Scan(func(e *store.Entry) error {
			if toolFilter != "" && e.Tool != toolFilter {
				return nil
			}
			if !includeExpired && now.After(e.ExpiresAt) {
				return nil
			}
			entries = append(entries, e)
			return nil
		})
	}

	w := os.Stdout
	switch {
	case format == "json":
		output.WriteInspectJSON(w, entries)
	case pretty:
		output.WriteInspectPretty(w, entries)
	default:
		output.WriteInspectEntries(w, entries)
	}
	return nil
}
