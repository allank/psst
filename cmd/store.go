package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
)

func init() {
	rootCmd.AddCommand(storeCmd)
	storeCmd.Flags().String("tool", "", "upstream tool name (required)")
	storeCmd.Flags().String("args", "", "arguments as JSON object (required)")
	storeCmd.Flags().String("result", "", "upstream tool response as JSON (required)")
	storeCmd.Flags().Int("ttl", 0, "TTL in seconds; 0 = use configured default for this tool")
	storeCmd.Flags().Bool("pretty", false, "human-readable output")
	storeCmd.Flags().String("store", "", "path to psst.db")
	_ = storeCmd.MarkFlagRequired("tool")
	_ = storeCmd.MarkFlagRequired("args")
	_ = storeCmd.MarkFlagRequired("result")
}

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Write a result into the cache",
	RunE:  runStore,
}

func runStore(cmd *cobra.Command, _ []string) error {
	toolName, _ := cmd.Flags().GetString("tool")
	argsRaw, _ := cmd.Flags().GetString("args")
	resultRaw, _ := cmd.Flags().GetString("result")
	ttl, _ := cmd.Flags().GetInt("ttl")
	pretty, _ := cmd.Flags().GetBool("pretty")
	storeFlag, _ := cmd.Flags().GetString("store")

	var args json.RawMessage
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		fmt.Fprintln(os.Stderr, "error: --args must be valid JSON")
		os.Exit(2)
	}
	var result json.RawMessage
	if err := json.Unmarshal([]byte(resultRaw), &result); err != nil {
		fmt.Fprintln(os.Stderr, "error: --result must be valid JSON")
		os.Exit(2)
	}

	cfg := loadConfig()
	storePath := resolveStorePath(storeFlag, cfg)

	s, err := openStore(storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	defer s.Close()

	emb, _ := loadEmbedder()
	if emb != nil {
		defer emb.Close()
	}

	c := openCache(s, cfg, emb, storePath)
	e, err := c.Store(cmd.Context(), toolName, args, result, ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	if emb != nil {
		_ = c.SaveSidecar()
	}

	w := os.Stdout
	if pretty {
		output.WriteStoredPretty(w, e)
	} else {
		output.WriteStored(w, e)
	}
	return nil
}
