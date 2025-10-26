package main

import (
	"fmt"
	"math/rand"
	"time"

	"Tendermint/internal/consensus"
	"Tendermint/internal/network"
	"Tendermint/internal/types"
)

func main() {
	powers := []int{3, 2, 2, 1, 1}
	totalNodes := len(powers)
	totalHeights := 3
	rand.Seed(time.Now().UnixNano())
	delay := func(int, int, types.Message) time.Duration {
		return time.Duration(10+rand.Intn(20)) * time.Millisecond
	}
	const logNetworkMessages = false

	opts := []network.NetworkOption{
		network.WithBroadcastDelay(delay),
		network.WithUnicastDelay(delay),
		network.WithGossipJitter(40 * time.Millisecond),
	}
	if logNetworkMessages {
		opts = append(opts, network.WithNetworkLogger(network.DefaultNetworkLogger))
	}

	net := network.NewSimulatedNetwork(8, opts...)
	defer net.Stop()

	behaviors := map[int]*consensus.ByzantineBehavior{
		0: {
			SilentPrevote:   true,
			SilentPrecommit: true,
		},
	}

	powerMap := make(map[int]int)
	for i, p := range powers {
		powerMap[i] = p
	}

	nodes := make([]*consensus.Node, totalNodes)
	for i := 0; i < totalNodes; i++ {
		node := consensus.NewNode(i, powers[i], powerMap)
		if b, ok := behaviors[i]; ok {
			node.SetBehavior(b)
			fmt.Printf("Node %d configured as Byzantine: %+v\n", i, *b)
		}
		node.SetNetwork(net)
		nodes[i] = node
	}

	for height := 1; height <= totalHeights; height++ {
		fmt.Printf("\n=== Starting height %d ===\n", height)
		success := consensus.RunConsensus(nodes, height, 1)
		if !success {
			fmt.Printf("[Height %d] Aborted: validators exceeded max consensus timeout\n", height)
			break
		}
	}

	time.Sleep(500 * time.Millisecond)
}
