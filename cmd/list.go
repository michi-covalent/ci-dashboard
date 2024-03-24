package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/go-github/v59/github"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list owner repo",
	Short: "List workflows",

	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			cmd.Usage()
			os.Exit(1)
		}
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			slog.Error("Set GITHUB_TOKEN environment variable")
			os.Exit(1)
		}
		client := github.NewClient(nil).WithAuthToken(token)
		owner := args[0]
		repo := args[1]
		ctx := context.Background()
		workflows, err := getWorkflows(ctx, client, owner, repo)
		if err != nil {
			return err
		}
		for _, workflow := range workflows {
			fmt.Println(workflow)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
