package tailout

import (
	"context"
	"fmt"
	"net/url"

	"github.com/lucacome/tailout/internal"
	tsapi "tailscale.com/client/tailscale/v2"
)

func (app *App) Disconnect(ctx context.Context) error {
	baseURL, err := url.Parse(app.Config.Tailscale.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse base URL: %w", err)
	}

	apiClient := &tsapi.Client{
		APIKey:  app.Config.Tailscale.APIKey,
		BaseURL: baseURL,
	}

	errUpdate := internal.UpdateExitNode(ctx, apiClient, "")
	if errUpdate != nil {
		return fmt.Errorf("failed to disconnect from exit node: %w", errUpdate)
	}

	fmt.Println("Disconnected from exit node.")
	return nil
}
