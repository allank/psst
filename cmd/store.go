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

	murlicobra.Annotate(storeCmd, murli.Metadata{
		AgentDescription: "Writes a tool result into the semantic cache database for a specified tool name and arguments. Auto-generates search embeddings if standard query keys are found.",
		WhenToUse:        "Use when you have executed an upstream tool call and want to cache the result so that future identical or semantically similar calls are cached.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "Cache storage status and expiration",
			Shape: map[string]any{
				"stored":  "bool (always true on success)",
				"key":     "string (unique SHA256 key of the cache entry)",
				"expires": "string (RFC3339 timestamp when entry will expire)",
			},
		},
		Examples: []string{
			"psst store --tool jira_get_issue --args '{\"issue_key\":\"PROJ-123\"}' --result '{\"summary\":\"OAuth mobile docs\"}'",
		},
	})
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

	writer := murlicobra.NewWriter(cmd)

	var args json.RawMessage
	if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
		return &murli.AgentError{
			Code:        murli.ExitUserError,
			ErrorType:   "invalid_args_json",
			Message:     "args flag must be a valid JSON object",
			Suggestion:  "Ensure the JSON is correctly formatted and properly escaped.",
			Recoverable: true,
		}
	}
	var result json.RawMessage
	if err := json.Unmarshal([]byte(resultRaw), &result); err != nil {
		return &murli.AgentError{
			Code:        murli.ExitUserError,
			ErrorType:   "invalid_result_json",
			Message:     "result flag must be a valid JSON object",
			Suggestion:  "Ensure the JSON result payload is correctly formatted.",
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
	e, err := c.Store(cmd.Context(), toolName, args, result, ttl)
	if err != nil {
		return &murli.AgentError{
			Code:        murli.ExitToolError,
			ErrorType:   "cache_store_error",
			Message:     fmt.Sprintf("failed to cache store result: %v", err),
			Recoverable: false,
		}
	}

	if emb != nil {
		_ = c.SaveSidecar()
	}

	w := os.Stdout
	if writer.IsTTY() {
		if pretty {
			output.WriteStoredPretty(w, e)
		} else {
			output.WriteStored(w, e)
		}
	} else {
		output.WriteStoredJSON(w, e)
	}
	return nil
}
