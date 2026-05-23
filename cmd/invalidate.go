package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
	"github.com/allank/psst/internal/output"
)

func init() {
	rootCmd.AddCommand(invalidateCmd)
	invalidateCmd.Flags().String("tool", "", "evict all entries for this tool")
	invalidateCmd.Flags().String("key", "", "evict specific entry by key")
	invalidateCmd.Flags().Bool("all", false, "flush entire cache")
	invalidateCmd.Flags().Bool("pretty", false, "human-readable output")
	invalidateCmd.Flags().String("store", "", "path to psst.db")

	murlicobra.Annotate(invalidateCmd, murli.Metadata{
		AgentDescription: "Evicts entries from the cache by tool name, specific entry key, or completely flushes the cache.",
		WhenToUse:        "Use when cached data is known to be stale or updated upstream, and needs to be evicted to force a fresh tool call on the next execution.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "Eviction summary status",
			Shape: map[string]any{
				"evicted": "bool (always true on success)",
				"tool":    "string (the target identifier cleared)",
				"count":   "int (total count of evicted cache records)",
			},
		},
		Examples: []string{
			"psst invalidate --tool jira_get_issue",
			"psst invalidate --all",
		},
	})
}

var invalidateCmd = &cobra.Command{
	Use:   "invalidate",
	Short: "Evict entries from the cache",
	RunE:  runInvalidate,
}

func runInvalidate(cmd *cobra.Command, _ []string) error {
	toolName, _ := cmd.Flags().GetString("tool")
	key, _ := cmd.Flags().GetString("key")
	all, _ := cmd.Flags().GetBool("all")
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

	c := openCache(s, cfg, nil, storePath)

	target := toolName
	if all {
		target = "*"
	}
	if toolName == "" && key == "" && !all {
		return &murli.AgentError{
			Code:        murli.ExitUserError,
			ErrorType:   "missing_args",
			Message:     "at least one of --tool, --key, or --all is required",
			Suggestion:  "Specify either a tool name to clear, an entry key, or --all to wipe everything.",
			Recoverable: true,
		}
	}

	count, err := c.Invalidate(target, key)
	if err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "eviction_error",
			Message:     fmt.Sprintf("eviction failed: %v", err),
			Recoverable: false,
		}
	}

	writer := murlicobra.NewWriter(cmd)
	w := os.Stdout
	if writer.IsTTY() {
		if pretty {
			output.WriteEvictedPretty(w, target, count)
		} else {
			output.WriteEvicted(w, target, count)
		}
	} else {
		output.WriteEvictedJSON(w, target, count)
	}
	return nil
}
