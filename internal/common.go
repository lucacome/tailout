package internal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/charmbracelet/huh"
	tslocal "tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

func GetRegions(ctx context.Context) ([]string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}
	ec2Svc := ec2.NewFromConfig(cfg)

	regions, err := ec2Svc.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to describe regions: %w", err)
	}

	regionNames := []string{}
	for _, region := range regions.Regions {
		regionNames = append(regionNames, *region.RegionName)
	}

	sort.Slice(regionNames, func(i, j int) bool {
		return regionNames[i] < regionNames[j]
	})

	return regionNames, nil
}

// Function that uses huh to return an AWS region fetched from the aws sdk.
func SelectRegion(ctx context.Context) (string, error) {
	regionNames, err := GetRegions(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("operation canceled: %w", ctx.Err())
		}
		return "", fmt.Errorf("failed to retrieve regions: %w", err)
	}

	var selectedRegion string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a region").
				Options(huh.NewOptions(regionNames...)...).
				Value(&selectedRegion),
		),
	)

	err = form.RunWithContext(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("region selection canceled: %w", ctxErr)
		}
		return "", fmt.Errorf("failed to select region: %w", err)
	}

	return selectedRegion, nil
}

// Function that uses huh to return a boolean value.
func PromptYesNo(ctx context.Context, question string) (bool, error) {
	var confirm bool

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(question).
				Value(&confirm),
		),
	)

	err := form.RunWithContext(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, fmt.Errorf("prompt canceled: %w", ctxErr)
		}
		return false, fmt.Errorf("failed to prompt for yes/no: %w", err)
	}

	return confirm, nil
}

func GetActiveNodes(ctx context.Context, c *tsapi.Client) ([]tsapi.Device, error) {
	devices, err := c.Devices().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}

	tailoutDevices := make([]tsapi.Device, 0)
	for _, device := range devices {
		for _, tag := range device.Tags {
			if tag == "tag:tailout" {
				if time.Duration(device.LastSeen.Minute()) < 10*time.Minute {
					tailoutDevices = append(tailoutDevices, device)
				}
			}
		}
	}

	return tailoutDevices, nil
}

func UpdateExitNode(ctx context.Context, c *tsapi.Client, id string) error {
	var localClient tslocal.Client

	status, err := localClient.Status(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tailscale status: %w", err)
	}

	if status.BackendState != "Running" {
		return errors.New("tailscale is not running")
	}

	var currentExitNodeName string
	if status.ExitNodeStatus != nil {
		// Get all devices to find the current exit node name
		devices, errList := c.Devices().List(ctx)
		if errList != nil {
			return fmt.Errorf("failed to get devices: %w", errList)
		}

		// Find the device that matches the current exit node ID
		for _, device := range devices {
			if device.NodeID == string(status.ExitNodeStatus.ID) {
				currentExitNodeName = device.Name
				break
			}
		}

		if currentExitNodeName != "" {
			fmt.Printf("Currently connected to exit node: %s\n", currentExitNodeName)
		}
	}

	prefs, err := localClient.GetPrefs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get prefs: %w", err)
	}

	if id != "" {
		fmt.Printf("Setting exit node to %s...\n", id)
		prefs.ExitNodeID = tailcfg.StableNodeID(id)
	} else {
		fmt.Println("Clearing exit node...")
		prefs.ClearExitNode()
	}
	_, err = localClient.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:         *prefs,
		ExitNodeIDSet: true,
	})
	if err != nil {
		return fmt.Errorf("failed to set/unset exit node: %w", err)
	}

	status, err = localClient.Status(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tailscale status: %w", err)
	}

	if status.ExitNodeStatus != nil && !status.ExitNodeStatus.Online {
		return errors.New("the exit node is not reachable")
	}

	return nil
}
