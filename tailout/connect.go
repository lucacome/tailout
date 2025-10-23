package tailout

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/charmbracelet/huh"
	"github.com/lucacome/tailout/internal"
	tsapi "tailscale.com/client/tailscale/v2"
)

func (app *App) Connect(ctx context.Context, args []string) error {
	var nodeConnect string

	nonInteractive := app.Config.NonInteractive

	baseURL, err := url.Parse(app.Config.Tailscale.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse base URL: %w", err)
	}

	apiClient := &tsapi.Client{
		APIKey:  app.Config.Tailscale.APIKey,
		Tailnet: app.Config.Tailscale.Tailnet,
		BaseURL: baseURL,
	}

	var deviceToConnectTo tsapi.Device

	tailoutDevices, err := internal.GetActiveNodes(ctx, apiClient)
	if err != nil {
		return fmt.Errorf("failed to get active nodes: %w", err)
	}

	switch {
	case len(args) != 0:
		nodeConnect = args[0]
		i := slices.IndexFunc(tailoutDevices, func(e tsapi.Device) bool {
			return e.Hostname == nodeConnect
		})
		if i == -1 {
			return fmt.Errorf("node %s not found", nodeConnect)
		}
		deviceToConnectTo = tailoutDevices[i]
		nodeConnect = deviceToConnectTo.NodeID
	case !nonInteractive:
		if len(tailoutDevices) == 0 {
			return errors.New("no tailout node found in your tailnet")
		}

		// Create options for huh select
		options := make([]huh.Option[int], len(tailoutDevices))
		for i, device := range tailoutDevices {
			// Get IP address if available
			addr := "no IP"
			if len(device.Addresses) > 0 {
				addr = device.Addresses[0]
			}
			// Display hostname and IP address
			label := fmt.Sprintf("%s (%s)", device.Hostname, addr)
			options[i] = huh.NewOption(label, i)
		}

		var selectedIndex int
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[int]().
					Title("Select a node to connect to").
					Options(options...).
					Value(&selectedIndex),
			),
		)

		err := form.RunWithContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to select node: %w", err)
		}

		deviceToConnectTo = tailoutDevices[selectedIndex]
		nodeConnect = deviceToConnectTo.NodeID
	default:
		return errors.New("no node name provided")
	}

	errUpdate := internal.UpdateExitNode(ctx, apiClient, nodeConnect)
	if errUpdate != nil {
		return fmt.Errorf("failed to connect to exit node: %w", errUpdate)
	}

	// Get IP address if available
	addr := "no IP"
	if len(deviceToConnectTo.Addresses) > 0 {
		addr = deviceToConnectTo.Addresses[0]
	}
	fmt.Printf("Connected to node %s (%s) via Tailscale.\n", deviceToConnectTo.Hostname, addr)

	return nil
}
