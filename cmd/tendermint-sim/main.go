package main

import (
	"fmt"
	"time"
)

func main() {
	cfg := SimulationConfig{
		Label:            "baseline",
		Powers:           []int{3, 2, 2, 1, 1, 1, 2, 4, 6, 8},
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
		},
	}

	summaryPath, timeoutPath, err := RunSimulation(cfg)
	if err != nil {
		fmt.Printf("simulation failed: %v\n", err)
		return
	}
	fmt.Printf("Consensus summary written to %s\n", summaryPath)
	fmt.Printf("Timeout summary written to %s\n", timeoutPath)
}
