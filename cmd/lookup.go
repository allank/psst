package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
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

	murlicobra.Annotate(lookupCmd, murli.Metadata{
		AgentDescription: "Checks the transparent cache for a prior result matching the specified tool name and arguments. Normalizes and hashes exact matches, and embeds query keys for semantic matches.",
		WhenToUse:        "Use when you want to retrieve a cached response for a tool call to avoid making expensive, slow, or redundant upstream API calls.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "Cache lookup status and cached response if found",
			Shape: map[string]any{
				"hit":     "bool (whether a cached match was found)",
				"match":   "string (either 'exact' or 'semantic')",
				"score":   "float (cosine similarity score, or null if exact)",
				"cached":  "string (RFC3339 timestamp when entry was cached)",
				"expires": "string (RFC3339 timestamp when entry expires)",
				"result":  "object (cached tool result JSON payload)",
			},
		},
		Examples: []string{
			"psst lookup --tool jira_get_issue --args '{\"issue_key\":\"PROJ-123\"}'",
			"psst lookup --tool slack_search --args '{\"q\":\"auth mobile\"}' --threshold 0.85",
		},
	})
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
	pretty, _ := cmd.Flags().GetBool("pretty")
	storeFlag, _ := cmd.Flags().GetString("store")

	writer := murlicobra.NewWriter(cmd)

	var args json.RawMessage
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return &murli.AgentError{
			Code:        murli.ExitUserError,
			ErrorType:   "invalid_args_json",
			Message:     "args flag must be a valid JSON object",
			Suggestion:  "Ensure the JSON is correctly formatted and properly escaped inside single quotes.",
			Recoverable: true,
		}
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
	defer s.Close()

	emb, _ := loadEmbedder()
	if emb != nil {
		defer emb.Close()
	}

	c := openCache(s, cfg, emb, storePath)

	r, err := c.Lookup(cmd.Context(), toolName, args, threshold)
	if err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "lookup_error",
			Message:     fmt.Sprintf("lookup operation failed: %v", err),
			Recoverable: false,
		}
	}

	w := os.Stdout
	if !r.Hit {
		if writer.IsTTY() {
			if pretty {
				output.WriteLookupPretty(w, r, false)
			} else {
				output.WriteLookupMiss(w)
			}
		} else {
			output.WriteLookupJSON(w, r, false)
		}
		murli.ExitFunc(1)
		return nil
	}

	if writer.IsTTY() {
		if pretty {
			output.WriteLookupPretty(w, r, true)
		} else {
			output.WriteLookupHit(w, r)
		}
	} else {
		output.WriteLookupJSON(w, r, true)
	}
	return nil // hit = exit 0
}
