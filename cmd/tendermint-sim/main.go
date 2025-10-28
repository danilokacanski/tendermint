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
		Heights:          3,
		DelayMinMs:       10,
		DelayMaxMs:       30,
		GossipJitter:     20 * time.Millisecond,
		ProposalTimeout:  750 * time.Millisecond,
		PrevoteTimeout:   650 * time.Millisecond,
		PrecommitTimeout: 650 * time.Millisecond,
		MaxTimeout:       20 * time.Second,
		Topology:         "ring",
		Byzantine: map[int]*ByzantineConfig{
			0: {
				SilentPrevote:   true,
				SilentPrecommit: true,
			},
			5: {
				SilentPrevote:   true,
				SilentPrecommit: true,
			},
		},
	}

	grid := SimulationGrid{
		// Heights:          []int{3},
		// GossipJitters:    []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond},
		// ProposalTimeouts: []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond},
		// Topologies:       []string{"ring", "full"},
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
