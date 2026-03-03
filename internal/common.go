package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/charmbracelet/huh"
	tslocal "tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

func getBroadRegion(region string) string {
	switch {
	case strings.HasPrefix(region, "us-gov-"):
		return "US GovCloud"
	case strings.HasPrefix(region, "us-"):
		return "United States"
	case strings.HasPrefix(region, "eu-"):
		return "Europe"
	case strings.HasPrefix(region, "ap-"):
		return "Asia Pacific"
	case strings.HasPrefix(region, "sa-"):
		return "South America"
	case strings.HasPrefix(region, "ca-"):
		return "Canada"
	case strings.HasPrefix(region, "af-"):
		return "Africa"
	case strings.HasPrefix(region, "me-"):
		return "Middle East"
	case strings.HasPrefix(region, "cn-"):
		return "China"
	case strings.HasPrefix(region, "il-"):
		return "Israel"
	case strings.HasPrefix(region, "mx-"):
		return "Mexico"
	default:
		return "Other"
	}
}

// getRegionDisplayNames fetches human-readable names for the given region codes
// from the AWS SSM Parameter Store global infrastructure parameters.
// It uses an already-loaded AWS config to avoid a redundant credential load.
func getRegionDisplayNames(ctx context.Context, cfg aws.Config, regionCodes []string) (map[string]string, error) {
	ssmSvc := ssm.NewFromConfig(cfg)

	names := make(map[string]string, len(regionCodes))

	// SSM GetParameters accepts at most 10 names per call.
	for i := 0; i < len(regionCodes); i += 10 {
		end := i + 10
		if end > len(regionCodes) {
			end = len(regionCodes)
		}
		batch := regionCodes[i:end]

		paths := make([]string, len(batch))
		for j, code := range batch {
			paths[j] = "/aws/service/global-infrastructure/regions/" + code + "/longName"
		}

		output, err := ssmSvc.GetParameters(ctx, &ssm.GetParametersInput{Names: paths})
		if err != nil {
			return nil, fmt.Errorf("failed to get region names from SSM: %w", err)
		}

		for _, param := range output.Parameters {
			// Path: /aws/service/global-infrastructure/regions/{code}/longName
			if param.Name == nil || param.Value == nil {
				continue
			}
			parts := strings.Split(*param.Name, "/")
			if len(parts) >= 7 {
				names[parts[5]] = *param.Value
			}
		}
	}

	return names, nil
}

func getRegionsWithConfig(ctx context.Context, cfg aws.Config) ([]string, error) {
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

func GetRegions(ctx context.Context) ([]string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}
	return getRegionsWithConfig(ctx, cfg)
}

// SelectRegion uses a two-step huh form to first pick a broad geographic area
// and then a specific region (shown with its human-readable name).
// Both steps are in a single form so the user can navigate back to correct a
// wrong broad-area selection.
func SelectRegion(ctx context.Context) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return "", fmt.Errorf("failed to load default config: %w", err)
	}

	regionCodes, err := getRegionsWithConfig(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("operation canceled: %w", ctx.Err())
		}
		return "", fmt.Errorf("failed to retrieve regions: %w", err)
	}

	// Group region codes by broad geographic area.
	broadMap := map[string][]string{}
	for _, code := range regionCodes {
		broad := getBroadRegion(code)
		broadMap[broad] = append(broadMap[broad], code)
	}

	broadRegions := make([]string, 0, len(broadMap))
	for broad := range broadMap {
		broadRegions = append(broadRegions, broad)
	}
	sort.Strings(broadRegions)

	// Fetch all display names upfront so OptionsFunc stays synchronous.
	// Treat SSM lookup as best-effort: if it fails for any non-context reason,
	// fall back to showing plain region codes.
	displayNames, err := getRegionDisplayNames(ctx, cfg, regionCodes)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("region selection canceled: %w", ctx.Err())
		}
		slog.Warn("failed to fetch region display names, falling back to region codes", "error", err)
		displayNames = map[string]string{}
	}

	var selectedBroad, selectedRegion string
	form := huh.NewForm(
		// Step 1: pick broad area.
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a geographic area").
				Options(huh.NewOptions(broadRegions...)...).
				Value(&selectedBroad),
		),
		// Step 2: pick specific region. OptionsFunc re-evaluates whenever
		// selectedBroad changes, enabling back navigation to step 1.
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a specific region").
				TitleFunc(func() string {
					return "Select a region in " + selectedBroad
				}, &selectedBroad).
				OptionsFunc(func() []huh.Option[string] {
					codes := broadMap[selectedBroad]
					options := make([]huh.Option[string], 0, len(codes))
					for _, code := range codes {
						label, ok := displayNames[code]
						if !ok {
							label = code
						} else {
							// Strip the broad prefix, e.g. "Asia Pacific (Tokyo)" → "Tokyo"
							if start := strings.LastIndex(label, "("); start != -1 {
								label = strings.TrimSuffix(label[start+1:], ")")
							}
							label = label + " — " + code
						}
						options = append(options, huh.NewOption(label, code))
					}
					return options
				}, &selectedBroad).
				Value(&selectedRegion),
		),
	)

	if err := form.RunWithContext(ctx); err != nil {
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
	if id != "" && (status.ExitNodeStatus == nil || status.ExitNodeStatus.ID != tailcfg.StableNodeID(id)) {
		return errors.New("failed to set the exit node")
	}

	if status.ExitNodeStatus != nil && !status.ExitNodeStatus.Online {
		return errors.New("the exit node is not reachable")
	}

	return nil
}
