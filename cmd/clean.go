package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/allank/murli"
	murlicobra "github.com/allank/murli/cobra"
)

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().String("store", "", "path to psst.db")

	murlicobra.Annotate(cleanCmd, murli.Metadata{
		AgentDescription: "Deletes the `psst.db` database and its corresponding sidecar index files.",
		WhenToUse:        "Use when you want to wipe clean the cache database, evicting all stored tool results and resetting the semantic index.",
		Idempotent:       true,
		Returns: &murli.ReturnSchema{
			Type:        "json",
			Description: "List of removed files",
			Shape: map[string]any{
				"removed": "bool (always true on success)",
				"paths":   "[]string (paths of deleted files)",
			},
		},
		Examples: []string{
			"psst clean",
		},
	})
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove the store and sidecar files",
	RunE:  runClean,
}

func runClean(cmd *cobra.Command, _ []string) error {
	writer := murlicobra.NewWriter(cmd)
	storeFlag, _ := cmd.Flags().GetString("store")
	cfg := loadConfig()
	storePath := resolveStorePath(storeFlag, cfg)
	sidecarPath := filepath.Join(filepath.Dir(storePath), "psst.idx")

	var removed []string
	for _, path := range []string{storePath, sidecarPath} {
		if err := os.Remove(path); err == nil {
			removed = append(removed, path)
		}
	}

	payload := map[string]any{
		"removed": true,
		"paths":   removed,
	}
	humanText := ""
	if len(removed) > 0 {
		var list []string
		for _, p := range removed {
			list = append(list, filepath.Base(p))
		}
		humanText = fmt.Sprintf("removed %s", filepath.Join(list...))
	} else {
		humanText = "no files to remove"
	}
	writer.WriteSuccess(humanText, payload)
	return nil
}
