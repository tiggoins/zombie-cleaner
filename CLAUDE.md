# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Kubernetes zombie process cleaner that runs as a DaemonSet on each node to automatically detect and clean up long-running zombie processes. The tool is designed to identify zombie processes, associate them with their respective containers and pods, and safely clean them up after multiple confirmations.

## Key Architecture Components

1. **Main Application** (`main.go`): Entry point that initializes components and manages graceful shutdown
2. **Configuration** (`internal/config`): Handles YAML config loading with environment variable overrides
3. **Process Detection** (`internal/detector`): Scans `/proc` to identify zombie processes and associates them with containers
4. **Cleanup Logic** (`internal/cleaner`): Implements the core cleanup logic with confirmation tracking and whitelisting
5. **Metrics** (`internal/metrics`): Prometheus metrics collection and HTTP server
6. **Logging** (`internal/logger`): Structured JSON logging using `slog`

## Development Commands

### Building
```bash
make build              # Build the binary
make docker-build       # Build Docker image
```

### Testing
```bash
make test               # Run all tests with coverage
```

### Local Development
```bash
make dev-run            # Build and run locally (requires Docker)
make logs               # View application logs
```

### Deployment
```bash
make deploy             # Deploy to Kubernetes
make undeploy           # Remove from Kubernetes
make status             # Check deployment status
```

### Configuration
```bash
make config-update      # Update configuration in Kubernetes
make dry-run            # Enable dry-run mode
make production         # Disable dry-run mode (production)
make debug              # Enable debug logging
```

## Common Development Tasks

### Running Tests
```bash
make test
```

### Building the Project
```bash
make build
```

### Updating Dependencies
```bash
make mod-tidy
```

### Code Linting
```bash
make lint
```

## Project Structure
```
zombie-cleaner/
├── main.go                 # Main application entry point
├── internal/
│   ├── config/             # Configuration management
│   ├── logger/             # Logging utilities
│   ├── metrics/            # Prometheus metrics
│   ├── detector/           # Zombie process detection
│   └── cleaner/            # Cleanup logic
├── config/                 # Configuration files
├── deploy/                 # Kubernetes deployment manifests
├── Dockerfile              # Container build definition
├── Makefile                # Build and development commands
└── README.md               # Project documentation
```

## Configuration

The application can be configured via:
1. YAML config file (`config/config.yaml`)
2. Environment variables (overrides config file)
3. Command-line flags (highest priority)

Key settings include check intervals, confirmation counts, timeouts, and whitelisting patterns.