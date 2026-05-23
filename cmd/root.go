package cmd

import (
	"github.com/spf13/cobra"

	murlicobra "github.com/allank/murli/cobra"
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "psst",
	Short: "Semantic MCP cache layer",
	Long:  "psst — check here first. Transparent semantic cache for MCP tool calls.",
}

func Execute() {
	_ = murlicobra.Execute(rootCmd)
}
