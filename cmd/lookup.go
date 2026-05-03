package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
)

func init() {
	rootCmd.AddCommand(lookupCmd)
	lookupCmd.Flags().String("tool", "", "upstream tool name (required)")
	lookupCmd.Flags().String("args", "", "arguments as JSON object (required)")
	lookupCmd.Flags().Float64("threshold", 0.82, "minimum cosine similarity for semantic hit")
	lookupCmd.Flags().String("format", "plain", "output format: plain, json")
	lookupCmd.Flags().Bool("pretty", false, "human-readable output")
	lookupCmd.Flags().String("store", "", "path to psst.db (overrides PSST_STORE and config)")
	_ = lookupCmd.MarkFlagRequired("tool")
	_ = lookupCmd.MarkFlagRequired("args")
}

var lookupCmd = &cobra.Command{
	Use:   "lookup",
	Short: "Check the cache for a prior result",
	RunE:  runLookup,
}

func runLookup(cmd *cobra.Command, _ []string) error {
	toolName, _ := cmd.Flags().GetString("tool")
	argsRaw, _ := cmd.Flags().GetString("args")
	threshold, _ := cmd.Flags().GetFloat64("threshold")
	format, _ := cmd.Flags().GetString("format")
	pretty, _ := cmd.Flags().GetBool("pretty")
	storeFlag, _ := cmd.Flags().GetString("store")

	var args json.RawMessage
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		fmt.Fprintln(os.Stderr, "error: --args must be valid JSON")
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

	r, err := c.Lookup(cmd.Context(), toolName, args, threshold)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	w := os.Stdout
	if !r.Hit {
		if format == "json" {
			output.WriteLookupJSON(w, r, false)
		} else if pretty {
			output.WriteLookupPretty(w, r, false)
		} else {
			output.WriteLookupMiss(w)
		}
		os.Exit(1) // miss = exit 1
	}

	if format == "json" {
		output.WriteLookupJSON(w, r, true)
	} else if pretty {
		output.WriteLookupPretty(w, r, true)
	} else {
		output.WriteLookupHit(w, r)
	}
	return nil // hit = exit 0
}
