package main

import (
	"fmt"
	"math/rand"
	"time"
)

func main() {
	powers := []int{1, 1, 2, 3, 3} // Total voting power = 10
	totalNodes := len(powers)
	totalHeights := 3
	delay := func(int, int, Message) time.Duration {
		return time.Duration(10+rand.Intn(20)) * time.Millisecond
	}
	const logNetworkMessages = false

	opts := []NetworkOption{
		WithBroadcastDelay(delay),
		WithUnicastDelay(delay),
		WithGossipJitter(40 * time.Millisecond),
	}
	if logNetworkMessages {
		opts = append(opts, WithNetworkLogger(DefaultNetworkLogger))
	}

	network := NewSimulatedNetwork(8, opts...)
	defer network.Stop()
	nodes := make([]*Node, totalNodes)

	behaviors := map[int]*ByzantineBehavior{
		0: {
			ForceNilPrevote:   true,
			ForceNilPrecommit: true,
		},
		3: {
			ForceNilPrevote:   true,
			ForceNilPrecommit: true,
		},
	}

	powerMap := make(map[int]int)
	for i, p := range powers {
		powerMap[i] = p
	}

	for i := 0; i < totalNodes; i++ {
		nodes[i] = &Node{
			ID:          i,
			In:          make(chan Message, 10),
			state:       &ValidatorState{},
			jailedPeers: make(map[int]bool),
			power:       powers[i],
			powerMap:    powerMap,
		}
		if b, ok := behaviors[i]; ok {
			nodes[i].behavior = b
			fmt.Printf("Node %d configured as Byzantine: %+v\n", i, *b)
		}
		network.Register(nodes[i])
	}

	for height := 1; height <= totalHeights; height++ {
		fmt.Printf("\n=== Starting height %d ===\n", height)
		success := RunConsensus(nodes, height, 1)
		if !success {
			fmt.Printf("[Height %d] Aborted: validators exceeded max consensus timeout\n", height)
			break
		}
	}

	time.Sleep(500 * time.Millisecond)
}
