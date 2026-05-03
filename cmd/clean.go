package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/allank/psst/internal/output"
)

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().String("store", "", "path to psst.db")
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove the store and sidecar files",
	RunE:  runClean,
}

func runClean(cmd *cobra.Command, _ []string) error {
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
	output.WriteRemoved(os.Stdout, removed)
	return nil
}
