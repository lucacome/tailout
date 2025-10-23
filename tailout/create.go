package tailout

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/charmbracelet/huh/spinner"
	"github.com/lucacome/tailout/internal"
	tsapi "tailscale.com/client/tailscale/v2"
)

var ErrUserAborted = errors.New("user aborted instance creation")

func (app *App) Create(ctx context.Context) error {
	nonInteractive := app.Config.NonInteractive
	region := app.Config.Region
	dryRun := app.Config.DryRun
	connect := app.Config.Create.Connect
	shutdown := app.Config.Create.Shutdown

	baseURL, err := url.Parse(app.Config.Tailscale.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse base URL: %w", err)
	}

	apiClient := &tsapi.Client{
		APIKey:  app.Config.Tailscale.APIKey,
		Tailnet: app.Config.Tailscale.Tailnet,
		BaseURL: baseURL,
	}

	keyCapabilities := tsapi.KeyCapabilities{
		Devices: struct {
			Create struct { //nolint:govet
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Tags          []string `json:"tags"`
				Preauthorized bool     `json:"preauthorized"`
			} `json:"create"`
		}{
			Create: struct { //nolint:govet
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Tags          []string `json:"tags"`
				Preauthorized bool     `json:"preauthorized"`
			}{
				Reusable:      false,
				Ephemeral:     true,
				Tags:          []string{"tag:tailout"},
				Preauthorized: true,
			},
		},
	}

	key, err := apiClient.Keys().Create(ctx, tsapi.CreateKeyRequest{
		Description:  "tailout",
		Capabilities: keyCapabilities,
	})
	if err != nil {
		return fmt.Errorf("failed to create auth key: %w", err)
	}

	// TODO: add option for no shutdown
	duration, err := time.ParseDuration(shutdown)
	if err != nil {
		return fmt.Errorf("failed to parse duration: %w", err)
	}

	durationMinutes := int(duration.Minutes())
	if durationMinutes < 1 {
		return errors.New("duration must be at least 1 minute")
	}

	// Create EC2 service client
	if region == "" && !nonInteractive {
		region, err = internal.SelectRegion(ctx)
		if err != nil {
			return fmt.Errorf("failed to select region: %w", err)
		}
	} else if region == "" && nonInteractive {
		return errors.New("selected non-interactive mode but no region was explicitly specified")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithRetryMaxAttempts(5), config.WithRetryMode(aws.RetryModeStandard))
	if err != nil {
		return fmt.Errorf("unable to load SDK config: %w", err)
	}

	runInput, errPrep := prepareInstance(ctx, cfg, aws.Bool(dryRun), strconv.Itoa(durationMinutes))
	if errPrep != nil {
		if errors.Is(errPrep, ErrUserAborted) {
			fmt.Println("Instance creation aborted.")
			return nil
		}
		return fmt.Errorf("failed to prepare instance: %w", errPrep)
	}
	if runInput == nil {
		fmt.Println("Instance creation aborted.")
		return nil
	}

	var publicIPAddress string
	var nodeName string
	var instanceID string
	s := spinner.New().Type(spinner.Dots).Title("Creating instance...")
	errSpin := s.Context(ctx).ActionWithErr(func(context.Context) error {
		instance, createErr := createInstance(ctx, cfg, runInput, s)
		if createErr != nil {
			return createErr
		}
		if instance.InstanceID == "" {
			return errors.New("instance creation aborted")
		}
		instanceID = instance.InstanceID
		nodeName = instance.Name
		publicIPAddress = instance.IP
		return nil
	}).Run()
	if errSpin != nil {
		return fmt.Errorf("failed to create instance: %w", errSpin)
	}

	// If dry run, exit here?

	st := spinner.New().Type(spinner.Dots).Title("Installing Tailscale...")
	errSpint := st.Context(ctx).ActionWithErr(func(context.Context) error {
		errInstall := installTailScale(ctx, cfg, key.Key, nodeName, instanceID, st)
		if errInstall != nil {
			return errInstall
		}
		return nil
	}).Run()
	if errSpint != nil {
		return fmt.Errorf("failed to install Tailscale: %w", errSpint)
	}

	fmt.Println("Tailscale installed.")

	nodes, deviceErr := apiClient.Devices().List(ctx)
	if deviceErr != nil {
		return fmt.Errorf("failed to get devices: %w", deviceErr)
	}

	found := false
	for _, node := range nodes {
		if node.Hostname == nodeName {
			fmt.Printf("Node %s joined tailnet.\n", nodeName)
			fmt.Println("Public IP address:", publicIPAddress)
			fmt.Println("Planned termination time:", time.Now().Add(duration).Format(time.RFC3339))
			found = true
			break
		}
	}
	if !found {
		return errors.New("failed to find the created node in tailnet")
	}

	if connect {
		fmt.Println()
		args := []string{nodeName}
		err = app.Connect(ctx, args)
		if err != nil {
			return fmt.Errorf("failed to connect to node: %w", err)
		}
	}
	return nil
}

type instance struct {
	InstanceID string
	Name       string
	IP         string
}

func prepareInstance(ctx context.Context, cfg aws.Config, dryRun *bool, shutdownDuration string) (instance *ec2.RunInstancesInput, err error) {
	ec2Svc := ec2.NewFromConfig(cfg)

	// DescribeImages to get the latest Amazon Linux AMI
	amazonLinuxImages, err := ec2Svc.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{"al2023-ami-*"},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
			{
				Name:   aws.String("is-public"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("architecture"),
				Values: []string{"x86_64"},
			},
		},
		Owners: []string{"amazon"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe Amazon Linux images: %w", err)
	}

	if len(amazonLinuxImages.Images) == 0 {
		return nil, errors.New("no Amazon Linux images found")
	}

	sort.Slice(amazonLinuxImages.Images, func(i, j int) bool {
		return *amazonLinuxImages.Images[i].CreationDate > *amazonLinuxImages.Images[j].CreationDate
	})

	// Get the latest Amazon Linux AMI ID
	latestAMI := amazonLinuxImages.Images[0]
	imageID := *latestAMI.ImageId
	imageName := *latestAMI.Name
	imageOwner := *latestAMI.ImageOwnerAlias
	imageArchitecture := latestAMI.Architecture

	// Define the instance details
	// TODO: add option for instance type
	// instanceType := "t3a.micro"
	// TODO: Fix shutdown time
	userDataScript := `#!/bin/bash
# Allow ip forwarding
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.conf
sudo sysctl -p /etc/sysctl.conf
sudo echo "sudo shutdown" | at now + ` + shutdownDuration + ` minutes`

	// Encode the string in base64
	userDataScriptBase64 := base64.StdEncoding.EncodeToString([]byte(userDataScript))

	// Create instance input parameters
	runInput := &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: types.InstanceTypeT3aMicro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(userDataScriptBase64),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags: []types.Tag{
					{
						Key:   aws.String("App"),
						Value: aws.String("tailout"),
					},
				},
			},
		},
		DryRun: dryRun,
		InstanceMarketOptions: &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
			SpotOptions: &types.SpotMarketOptions{
				InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate,
			},
		},
	}

	stsSvc := sts.NewFromConfig(cfg)

	identity, err := stsSvc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get account ID: %w", err)
	}

	fmt.Printf(`Creating tailout node in AWS with the following parameters:
- AWS Account ID: %s
- AMI ID: %s (%s by %s)
- AMI Architecture: %s
- Instance Type: %s
- Region: %s
- Auto shutdown after: %s
- Network: default VPC / Subnet / Security group of the region
	`, *identity.Account, imageID, imageName, imageOwner, imageArchitecture, types.InstanceTypeT3aMicro, cfg.Region, shutdownDuration)

	result, promptErr := internal.PromptYesNo(ctx, "Do you want to create this instance?")
	if promptErr != nil {
		return nil, fmt.Errorf("failed to prompt for confirmation: %w", promptErr)
	}

	if !result {
		return nil, ErrUserAborted
	}
	return runInput, nil
}

func createInstance(ctx context.Context, cfg aws.Config, runInput *ec2.RunInstancesInput, spin *spinner.Spinner) (instance instance, err error) {
	ec2Svc := ec2.NewFromConfig(cfg)

	// Run the EC2 instance
	runResult, runErr := ec2Svc.RunInstances(ctx, runInput)
	if runErr != nil {
		var dryRunErr *smithy.GenericAPIError
		if errors.As(runErr, &dryRunErr) && dryRunErr.Code == "DryRunOperation" {
			fmt.Println("Dry run successful. Instance can be created.")
			return instance, nil
		}
		return instance, fmt.Errorf("failed to create EC2 instance: %w", runErr)
	}

	if len(runResult.Instances) == 0 {
		fmt.Println("No instances created.")
		return instance, nil
	}
	createdInstance := runResult.Instances[0]

	fmt.Println("Instance created:", *createdInstance.InstanceId)

	nodeName := fmt.Sprintf("tailout-%s-%s", cfg.Region, *createdInstance.InstanceId)
	// Create tags for the instance
	tags := []types.Tag{
		{
			Key:   aws.String("Name"),
			Value: aws.String(nodeName),
		},
	}

	// Add the tags to the instance
	_, err = ec2Svc.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{*createdInstance.InstanceId},
		Tags:      tags,
	})
	if err != nil {
		return instance, fmt.Errorf("failed to add tags to the instance: %w", err)
	}

	spin.Title("Waiting for instance to be running...")
	err = ec2.NewInstanceStatusOkWaiter(ec2Svc).Wait(ctx, &ec2.DescribeInstanceStatusInput{
		InstanceIds: []string{*createdInstance.InstanceId},
	}, time.Minute*5)
	if err != nil {
		return instance, fmt.Errorf("failed to wait for instance to be created: %w", err)
	}

	describeInput := &ec2.DescribeInstancesInput{
		InstanceIds: []string{*createdInstance.InstanceId},
	}

	describeResult, err := ec2Svc.DescribeInstances(ctx, describeInput)
	if err != nil {
		return instance, fmt.Errorf("failed to describe EC2 instance: %w", err)
	}

	if len(describeResult.Reservations) == 0 {
		return instance, errors.New("no reservations found")
	}

	reservation := describeResult.Reservations[0]
	if len(reservation.Instances) == 0 {
		return instance, errors.New("no instances found")
	}

	instance1 := reservation.Instances[0]
	if instance1.PublicIpAddress == nil {
		return instance, errors.New("no public IP address found")
	}

	instance.Name = nodeName
	instance.InstanceID = *createdInstance.InstanceId
	instance.IP = *instance1.PublicIpAddress

	return instance, nil
}

func installTailScale(ctx context.Context, cfg aws.Config, key string, nodeName string, instanceID string, spin *spinner.Spinner) error {
	ssmSvc := ssm.NewFromConfig(cfg)

	input := &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {
				"echo 'Installing Tailscale...'",
				"curl -fsSL https://tailscale.com/install.sh | sh",
				"echo 'Starting Tailscale...'",
				"sudo tailscale up --auth-key=" + key + " --hostname=" + nodeName + " --advertise-exit-node --ssh",
				"echo 'Tailscale installation and configuration completed.'",
			},
		},
	}

	spin.Title("Installing Tailscale...")
	output, err := ssmSvc.SendCommand(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send SSM command: %w", err)
	}

	commandID := *output.Command.CommandId
	waiter := ssm.NewCommandExecutedWaiter(ssmSvc)
	waitErr := waiter.Wait(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  aws.String(commandID),
		InstanceId: aws.String(instanceID),
	}, 5*time.Minute)
	if waitErr != nil {
		return fmt.Errorf("failed to wait for SSM command execution: %w", waitErr)
	}

	invocationOutput, err := ssmSvc.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  aws.String(commandID),
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		return fmt.Errorf("failed to get SSM command invocation: %w", err)
	}

	if invocationOutput.Status != ssmTypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("SSM command failed with status: %s, output: %s", invocationOutput.Status, aws.ToString(invocationOutput.StandardErrorContent))
	}

	return nil
}
