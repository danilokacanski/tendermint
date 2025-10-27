# Tendermint Simulator

This repository provides a self-contained Tendermint-style consensus simulator. It runs several validators inside one Go process with a pluggable simulated network, supports Byzantine behaviours, and exposes a small suite of integration tests.

## Requirements

- [Go](https://go.dev/) **1.21 or newer** (any release that supports Go modules and `go test ./...`).
- macOS, Linux, or Windows (only standard library dependencies).

## Project Layout

```
cmd/tendermint-sim/    # CLI entrypoint: wires validators, network, behaviours
internal/consensus/    # Node state machine, voting logic, round management
internal/network/      # Simulated gossip transport with latency/jitter control, peer topology, signature verification, misbehaviour handling
internal/types/        # Shared message and colour definitions
test/                  # Integration scenarios exercising the protocol behaviour
```

## Getting Started

1. Install Go if you do not already have it:
   ```sh
   # macOS (Homebrew)
   brew install go

   # Linux (Debian/Ubuntu)
   sudo apt-get install golang
   ```

2. Clone the repository or pull the sources onto your machine.

3. Fetch dependencies (standard Go tooling handles this automatically):
   ```sh
   go mod tidy
   ```

## Running the Simulator

From the repository root, execute:

```sh
go run ./cmd/tendermint-sim
```

This boots the built-in scenario (five validators with configurable Byzantine behaviour) and prints the consensus log for a few heights.

## Running Tests

To execute the deterministic integration tests:

```sh
go test ./...
```

Add `-count=1` to bypass Go’s cache if you want a fresh run every time:

```sh
go test -count=1 ./...
```

The tests spin up validators with deterministic network settings and verify core properties (quorum math, successful consensus with an honest majority, and abort behaviour when Byzantine power exceeds one-third).

## Customising

- Adjust validator powers and Byzantine behaviours in `cmd/tendermint-sim/main.go`.
- Tune network latency, jitter, logging, peer topology, and signature validation options using helpers in `internal/network`.
- Extend the test scenarios under `test/` to cover new edge cases or protocol changes.
