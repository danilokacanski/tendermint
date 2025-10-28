package main

import (
	"fmt"
	"time"
)

// main defines a baseline configuration and a small grid of variations, then
// executes every combination and prints the generated report paths.
func main() {
	base := SimulationConfig{
		Label:            "baseline",
		Powers:           []int{3, 2, 2, 1, 1, 1},
		Heights:          5,
		DelayMinMs:       10,
		DelayMaxMs:       30,
		GossipJitter:     20 * time.Millisecond,
		ProposalTimeout:  750 * time.Millisecond,
		PrevoteTimeout:   650 * time.Millisecond,
		PrecommitTimeout: 650 * time.Millisecond,
		MaxTimeout:       0,
		Topology:         "ring",
		Byzantine: map[int]*ByzantineConfig{
			0: {
				SilentPrevote:   true,
				SilentPrecommit: true,
			},
		},
	}

	grid := SimulationGrid{
		Heights:           []int{5},
		GossipJitters:     []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 75 * time.Millisecond},
		ProposalTimeouts:  []time.Duration{400 * time.Millisecond, 600 * time.Millisecond},
		PrevoteTimeouts:   []time.Duration{400 * time.Millisecond, 600 * time.Millisecond},
		PrecommitTimeouts: []time.Duration{400 * time.Millisecond, 600 * time.Millisecond},
		Topologies:        []string{"ring"},
	}

	results, err := RunSimulationGrid(base, grid)
	if err != nil {
		fmt.Printf("simulation grid failed: %v\n", err)
		return
	}

	if len(results) == 0 {
		fmt.Println("no simulations executed")
		return
	}

	for _, res := range results {
		fmt.Printf("[%s] consensus summary: %s\n", res.Config.Label, res.ConsensusSummary)
		fmt.Printf("[%s] timeout summary:   %s\n", res.Config.Label, res.TimeoutSummary)
	}
}
