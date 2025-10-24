# tailout

[![CI](https://github.com/lucacome/tailout/actions/workflows/ci.yaml/badge.svg)](https://github.com/lucacome/tailout/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/lucacome/tailout)](https://goreportcard.com/report/github.com/lucacome/tailout)

`tailout` is a command-line tool for quickly creating a cloud-based exit node in your tailnet.

![demo gif](./docs/demo.gif)

## Installation

To install `tailout`, you can download the latest release from the [releases page](https://github.com/lucacome/tailout/releases).

You can also use the `go install` command:

```bash
go install github.com/lucacome/tailout@latest
```

## Prerequisites

To use `tailout`, you'll need to have the following installed:

- [Tailscale](https://tailscale.com/)
- An AWS account

At the moment, `tailout` only supports AWS as a cloud provider. Support for other cloud providers will be added in the future.

## Setup

Go to your [Tailscale API key settings](https://login.tailscale.com/admin/settings/keys) and:

- Create an API key for `tailout`
- Create a file in `~/.tailout/config.yaml` with the following content:

  ```yaml
  tailscale:
    api_key: tskey-api-xxx-xxx
  ```

- Run `tailout init`, review the changes that will be done to your policy and accept

Next, you will also need to set up your AWS credentials. tailout will look for default credentials,
like environment variables for access keys or an AWS profile.

To easily check if your credentials are set up correctly, you can use the `aws sts get-caller-identity` command.

## Usage

Create an exit node in your tailnet:

```bash
tailout create
```

Connect to your exit node:

```bash
tailout connect
```

Get the status of your exit node:

```bash
tailout status
```

Disconnect from your exit node:

```bash
tailout disconnect
```

Delete your exit node:

```bash
tailout stop
```

## Configuration

`tailout` will look for a configuration file at the following paths:

- `/etc/tailout/config.{yml,yaml,hcl,json,toml}`
- `$HOME/.tailout/config.{yml,yaml,hcl,json,toml}`

For exemple, you could have this content in `/etc/tailout/config.yml`:

```yaml
tailscale:
  api_key: tskey-api-xxx-xxx
region: eu-west-3
create:
  shutdown: 15m
```

You can specify any of the above settings as command-line flags or environment variables prefixed by `TAILOUT_`.

For example, to specify the Tailscale API key, you can use the `--tailscale-api-key` flag or
the `TAILOUT_TAILSCALE_API_KEY` environment variable.

## License

This repository contains code under the following terms:

1. Portions derived from [cterence/tailout](https://github.com/cterence/tailout)
remain licensed under the Apache License 2.0 (original work by the upstream authors).
2. New contributions to this fork are made available under either the Apache License 2.0 OR the MIT License, at your option.

See [LICENSE-APACHE](LICENSE-APACHE) and [LICENSE-MIT](LICENSE-MIT) for full license texts.
