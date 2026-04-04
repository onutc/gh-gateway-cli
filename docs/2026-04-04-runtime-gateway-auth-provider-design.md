---
date: 2026-04-04
author: Onur Solmaz <onur@textcortex.com>
title: Runtime Gateway Auth Provider Design
tags: [gh-gateway-cli, auth, gateway, git, design]
---

## Overview

This document describes how to move the current runtime-side helper behavior
into `gh-gateway-cli` without turning the CLI into a TextCortex-specific tool.

The immediate motivator is tcdev:

- `gh` should work out of the box against a GitHub-compatible gateway
- `git` should also work out of the box through normal credential-helper wiring
- no separate platform wrapper should be required around `gh`
- no platform-owned helper script should need to ship in the runtime image

The design goal is to absorb only the generic token-provider behavior into the
CLI while keeping runtime-specific exchange details configurable.

## Current Problem

Today tcdev relies on a separate helper script to bridge from runtime identity
to gateway access.

That helper currently does four jobs:

1. read a workload or runtime identity token from the environment or filesystem
2. exchange that token for a runtime-scoped access token
3. exchange that runtime token for a GitHub-gateway access token
4. print the result in one of two formats:
   - raw token output for `gh`
   - Git credential-helper output for `git`

This works, but it has three drawbacks:

- the auth logic lives outside the CLI
- tcdev has to ship platform-owned helper code
- `gh` and `git` end up depending on an extra bootstrap layer

## Design Goal

`gh-gateway-cli` should own gateway token retrieval directly.

The CLI should support:

- runtime-friendly token exchange flows
- short-lived token caching and refresh
- raw token output for CLI API calls
- Git credential-helper output for `git`

The CLI should not:

- hardcode TextCortex endpoint names
- hardcode Spritz request or response schemas into the core command layer
- require a mutable config directory just to work in ephemeral runtimes

## Target User Experience

The target tcdev setup is:

- `gh` is `gh-gateway-cli`
- Git remotes still look like normal GitHub remotes
- `git credential.helper` points back to `gh`
- no wrapper binary is installed
- no separate JS helper file is installed

The runtime should only need:

- injected environment variables
- the service-account token file already present in the pod
- standard Git credential-helper configuration

## Proposed CLI Surface

Add a gateway-oriented command group to the CLI.

Recommended subcommands:

- `gh gateway token`
  - prints a gateway access token to stdout
- `gh gateway credential`
  - speaks Git credential-helper protocol on stdin/stdout
- `gh gateway env`
  - optional diagnostics command to show resolved config without printing secrets

For tcdev, the main bootstrap wiring then becomes:

```bash
git config --global credential.helper '!gh gateway credential'
```

and normal CLI commands continue to use the configured gateway base URLs.

## Provider Boundary

To stay agnostic, `gh-gateway-cli` should not implement one hardcoded
TextCortex auth flow in the command handlers.

Instead, introduce a provider boundary with two layers:

1. generic runtime-gateway contract inside the CLI
2. provider-specific configuration that tells the CLI how to execute the flow

The generic contract is:

- resolve the upstream gateway base URL
- acquire an access token
- cache it until refresh is needed
- return the token in either raw-token or Git-credential format

The provider-specific layer supplies:

- token source type
- exchange endpoint URLs
- request templates or provider implementation
- response field mapping
- cache key identity

## TextCortex Compatibility

TextCortex can fit this model without polluting the CLI core.

The tcdev runtime-specific inputs are things like:

- runtime API base URL
- runtime exchange URL
- gateway exchange URL
- instance identifier
- workload token file path

Those are configuration inputs, not product-specific command semantics.

The CLI can remain generic if TextCortex support is implemented as one provider
among potentially many, selected by config.

That means:

- the CLI stays reusable outside TextCortex
- tcdev still gets first-class support
- future gatewayed environments can reuse the same mechanism

## Configuration Model

The primary configuration source should remain environment variables.

Reasons:

- tcdev is an ephemeral runtime
- pod env is already the deployment source of truth
- environment-driven bootstrap is easier to reason about than mutable local
  config files
- it avoids drift between runtime state and checked-in deployment config

An optional config file can still exist for local developer overrides, but it
should not be required for runtime environments.

Recommended precedence:

1. explicit CLI flags
2. environment variables
3. optional XDG config file
4. built-in defaults

## Caching

The CLI should continue to cache short-lived gateway tokens locally.

Requirements:

- cache by provider and effective identity inputs
- refresh before expiry using a small skew window
- never require long-lived tokens to be stored in config
- keep cache format private to the CLI

This preserves good runtime behavior without requiring platform helper scripts.

## Git Integration

Git still needs a credential helper. That is normal and should remain.

The goal is not to eliminate Git credential-helper usage.
The goal is to eliminate the extra platform-owned helper implementation.

The desired end state is:

- `git` asks `gh gateway credential`
- `gh-gateway-cli` resolves or refreshes a gateway token
- `gh-gateway-cli` prints Git credential-helper output

That keeps the integration standard while consolidating the logic in one tool.

## Non-Goals

This design should not:

- add TextCortex-specific command names to the public CLI surface
- require tcdev-specific wrapper binaries
- require a dedicated runtime config directory just to make auth work
- store static GitHub credentials inside tcdev

## Validation

Validation should cover both generic CLI behavior and tcdev compatibility.

### CLI tests

- provider config resolution
- token command output
- Git credential-helper output
- token caching and refresh behavior
- missing-config and malformed-response failures

### tcdev integration checks

- `gh api repos/{owner}/{repo}` works through the gateway
- `git fetch` works through the gateway
- `git push` works through the gateway
- `gh pr create` works without extra wrappers

## Recommended Next Step

Implement the gateway provider interface in `gh-gateway-cli` first, then reduce
platform bootstrap to:

- install `gh-gateway-cli`
- export provider configuration
- point `git credential.helper` to `gh gateway credential`

Only after that should platform-side helper code be removed.
