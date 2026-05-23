package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
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

	murlicobra.Annotate(inspectCmd, murli.Metadata{
		AgentDescription: "Lists or inspects cached database entries, filtering by tool name or key. Optionally includes expired cache records.",
		WhenToUse:        "Use when you want to audit what tools are currently cached, read the exact cached arguments and keys, or inspect a single entry by key.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "List of matched cache entries",
		},
		Examples: []string{
			"psst inspect",
			"psst inspect --tool confluence_search",
			"psst inspect --expired",
		},
	})
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

	var entries []*store.Entry
	now := time.Now()

	if keyFilter != "" {
		e, err := s.Get(keyFilter)
		if err != nil {
			return &murli.AgentError{
				Code:        murli.ExitToolError,
				ErrorType:   "store_get_error",
				Message:     fmt.Sprintf("failed to retrieve entry: %v", err),
				Recoverable: false,
			}
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

	writer := murlicobra.NewWriter(cmd)
	w := os.Stdout
	if writer.IsTTY() {
		if pretty {
			output.WriteInspectPretty(w, entries)
		} else {
			output.WriteInspectEntries(w, entries)
		}
	} else {
		output.WriteInspectJSON(w, entries)
	}
	return nil
}
