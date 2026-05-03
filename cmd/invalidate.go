package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
)

func init() {
	rootCmd.AddCommand(invalidateCmd)
	invalidateCmd.Flags().String("tool", "", "tool name to invalidate")
	invalidateCmd.Flags().String("key", "", "specific cache key to evict")
	invalidateCmd.Flags().Bool("all", false, "flush entire cache")
	invalidateCmd.Flags().Bool("pretty", false, "human-readable output")
	invalidateCmd.Flags().String("store", "", "path to psst.db")
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
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	defer s.Close()

	c := openCache(s, cfg, nil, storePath)

	target := toolName
	if all {
		target = "*"
	}
	if target == "" && key == "" {
		fmt.Fprintln(os.Stderr, "error: specify --tool, --key, or --all")
		os.Exit(2)
	}

	n, err := c.Invalidate(target, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	label := target
	if key != "" {
		label = key
	}
	if pretty {
		output.WriteEvictedPretty(os.Stdout, label, n)
	} else {
		output.WriteEvicted(os.Stdout, label, n)
	}
	return nil
}
