package test

import (
	"testing"
	"time"

	"Tendermint/internal/consensus"
	"Tendermint/internal/network"
	"Tendermint/internal/types"
)

func TestRecomputeQuorumToleratesByzantinePower(t *testing.T) {
	powerMap := map[int]int{0: 3, 1: 2, 2: 2, 3: 1, 4: 1}
	node := consensus.NewNode(0, powerMap[0], powerMap)
	node.RecomputeQuorum()
	if node.QuorumPower() != 6 {
		t.Fatalf("expected quorum 6, got %d", node.QuorumPower())
	}

	node.JailPeer(3)
	node.RecomputeQuorum()
	if node.QuorumPower() != 6 {
		t.Fatalf("expected quorum 6 after jailing validator 3, got %d", node.QuorumPower())
	}
}

func TestConsensusCommitsWithHonestMajority(t *testing.T) {
	powers := []int{3, 2, 2, 1, 1}
	net := newDeterministicNetwork()
	defer net.Stop()

	nodes := makeNodesWithPowers(powers, net, nil)

	if success := consensus.RunConsensus(nodes, 1, 1); !success {
		t.Fatalf("expected consensus to succeed with honest majority")
	}

	block := nodes[0].CommittedBlock()
	if block == "" {
		t.Fatalf("expected node 0 to commit a block")
	}

	for _, n := range nodes {
		if !n.Committed() {
			t.Fatalf("node %d did not mark itself committed", n.Identifier())
		}
		if n.CommittedBlock() != block {
			t.Fatalf("node %d committed %q, expected %q", n.Identifier(), n.CommittedBlock(), block)
		}
	}
}

func TestConsensusAbortsWhenFaultsExceedThreshold(t *testing.T) {
	powers := []int{2, 2, 2, 2, 1, 1}
	behaviors := map[int]*consensus.ByzantineBehavior{
		0: {ForceNilPrevote: true, ForceNilPrecommit: true},
		1: {ForceNilPrevote: true, ForceNilPrecommit: true},
	}
	net := newDeterministicNetwork()
	defer net.Stop()

	nodes := makeNodesWithPowers(powers, net, behaviors)
	for _, n := range nodes {
		n.SetMaxTimeout(300 * time.Millisecond)
		n.SetTimeouts(20*time.Millisecond, 30*time.Millisecond, 40*time.Millisecond)
	}

	if success := consensus.RunConsensus(nodes, 1, 1); success {
		t.Fatalf("expected consensus to abort with >1/3 Byzantine power")
	}

	abortedCount := 0
	for _, n := range nodes {
		if !n.Committed() && n.Aborted() {
			abortedCount++
		}
	}
	if abortedCount == 0 {
		t.Fatalf("expected at least one validator to abort after exceeding max timeout")
	}
}

func makeNodesWithPowers(powers []int, net *network.SimulatedNetwork, behaviors map[int]*consensus.ByzantineBehavior) []*consensus.Node {
	powerMap := make(map[int]int, len(powers))
	for i, p := range powers {
		powerMap[i] = p
	}

	nodes := make([]*consensus.Node, len(powers))
	for i := range powers {
		node := consensus.NewNode(i, powers[i], powerMap)
		if behaviors != nil {
			if b, ok := behaviors[i]; ok {
				node.SetBehavior(b)
			}
		}
		node.SetNetwork(net)
		nodes[i] = node
	}
	return nodes
}

func newDeterministicNetwork() *network.SimulatedNetwork {
	zeroDelay := func(from, to int, msg types.Message) time.Duration { return 0 }
	return network.NewSimulatedNetwork(
		8,
		network.WithBroadcastDelay(zeroDelay),
		network.WithUnicastDelay(zeroDelay),
		network.WithGossipJitter(0),
		network.WithRandomSeed(1),
	)
}
