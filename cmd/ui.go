package cmd

import (
	"fmt"

	"github.com/lucacome/tailout/tailout"
	"github.com/spf13/cobra"
)

// uiCmd represents the UI command.
func buildUICommand(app *tailout.App) *cobra.Command {
	cmd := &cobra.Command{
		Args:  cobra.ArbitraryArgs,
		Use:   "ui",
		Short: "Start the Tailout UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := app.UI(cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to start UI: %w", err)
			}
			return nil
		},
	}

	cmd.PersistentFlags().BoolVarP(&app.Config.NonInteractive, "non-interactive", "n", false, "Disable interactive prompts")
	cmd.PersistentFlags().StringVarP(&app.Config.UI.Address, "address", "a", "127.0.0.1", "Address to bind the UI to")
	cmd.PersistentFlags().StringVarP(&app.Config.UI.Port, "port", "p", "3000", "Port to bind the UI to")

	return cmd
}
