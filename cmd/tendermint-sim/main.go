package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"Tendermint/internal/consensus"
	"Tendermint/internal/network"
	"Tendermint/internal/types"
)

func main() {
	powers := []int{3, 2, 2, 1, 1, 1}
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
		network.WithGossipJitter(20 * time.Millisecond),
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
	peerMap := buildRingPeers(totalNodes)

	nodes := make([]*consensus.Node, totalNodes)
	for i := 0; i < totalNodes; i++ {
		node := consensus.NewNode(i, powers[i], powerMap)
		if b, ok := behaviors[i]; ok {
			node.SetBehavior(b)
			fmt.Printf("Node %d configured as Byzantine: %+v\n", i, *b)
		}
		node.SetTimeouts(750*time.Millisecond, 650*time.Millisecond, 650*time.Millisecond)
		node.SetMaxTimeout(20 * time.Second)
		node.SetNetwork(net, peerMap[i])
		nodes[i] = node
	}

	summaries := make([]*consensus.HeightSummary, 0, totalHeights)
	for height := 1; height <= totalHeights; height++ {
		fmt.Printf("\n=== Starting height %d ===\n", height)
		summary, success := consensus.RunConsensus(nodes, height, 1)
		if summary != nil {
			summaries = append(summaries, summary)
		}
		if !success {
			fmt.Printf("[Height %d] Aborted: validators exceeded max consensus timeout\n", height)
			break
		}
	}

	if len(summaries) > 0 {
		if path, err := writeConsensusSummaryCSV(summaries); err != nil {
			fmt.Printf("failed to write consensus summary: %v\n", err)
		} else {
			fmt.Printf("Consensus summary written to %s\n", path)
		}
	}

	time.Sleep(500 * time.Millisecond)
}

func buildRingPeers(total int) map[int][]int {
	if total <= 0 {
		return nil
	}
	if total == 1 {
		return map[int][]int{0: nil}
	}
	peers := make(map[int][]int, total)
	for i := 0; i < total; i++ {
		left := (i - 1 + total) % total
		right := (i + 1) % total
		peers[i] = []int{left, right}
	}
	return peers
}

func writeConsensusSummaryCSV(summaries []*consensus.HeightSummary) (string, error) {
	dir := filepath.Join("summary")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating summary directory: %w", err)
	}

	filename := time.Now().Format("20060102_150405") + ".csv"
	path := filepath.Join(dir, filename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating summary file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"height",
		"success",
		"rounds_attempted",
		"commit_round",
		"commit_block",
		"commit_proposer",
		"commit_duration_ms",
		"locked_round",
		"locked_value",
		"valid_round",
		"valid_value",
	}
	if err := writer.Write(header); err != nil {
		return "", fmt.Errorf("writing header: %w", err)
	}

	for _, s := range summaries {
		record := []string{
			fmt.Sprintf("%d", s.Height),
			fmt.Sprintf("%t", s.Success),
			fmt.Sprintf("%d", s.Rounds),
			fmt.Sprintf("%d", s.CommitRound),
			s.CommitBlock,
			fmt.Sprintf("%d", s.CommitProposer),
			fmt.Sprintf("%d", s.CommitDuration.Milliseconds()),
			fmt.Sprintf("%d", s.LockedRound),
			s.LockedValue,
			fmt.Sprintf("%d", s.ValidRound),
			s.ValidValue,
		}
		if err := writer.Write(record); err != nil {
			return "", fmt.Errorf("writing record for height %d: %w", s.Height, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("flushing csv writer: %w", err)
	}

	return path, nil
}
