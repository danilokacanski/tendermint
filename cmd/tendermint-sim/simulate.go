package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"Tendermint/internal/consensus"
	"Tendermint/internal/network"
	"Tendermint/internal/types"
)

type ByzantineConfig struct {
	EquivocatePrevote   bool
	EquivocatePrecommit bool
	ForceNilPrevote     bool
	ForceNilPrecommit   bool
	SilentPrevote       bool
	SilentPrecommit     bool
}

type SimulationConfig struct {
	Label            string
	Powers           []int
	Heights          int
	Seed             int64
	DelayMinMs       int
	DelayMaxMs       int
	GossipJitter     time.Duration
	ProposalTimeout  time.Duration
	PrevoteTimeout   time.Duration
	PrecommitTimeout time.Duration
	MaxTimeout       time.Duration
	Topology         string
	Byzantine        map[int]*ByzantineConfig
	NetworkWorkers   int
	LogNetwork       bool
}

func RunSimulation(cfg SimulationConfig) (string, string, error) {
	if len(cfg.Powers) == 0 {
		return "", "", fmt.Errorf("simulation requires at least one validator power entry")
	}
	if cfg.Heights <= 0 {
		cfg.Heights = 1
	}
	if cfg.NetworkWorkers <= 0 {
		cfg.NetworkWorkers = 8
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	cfg.Seed = seed

	rng := rand.New(rand.NewSource(seed))

	minDelay := cfg.DelayMinMs
	maxDelay := cfg.DelayMaxMs
	if maxDelay < minDelay {
		maxDelay = minDelay
	}

	delayFn := func(int, int, types.Message) time.Duration {
		if maxDelay <= minDelay {
			return time.Duration(minDelay) * time.Millisecond
		}
		delta := rng.Intn(maxDelay-minDelay+1) + minDelay
		return time.Duration(delta) * time.Millisecond
	}

	opts := []network.NetworkOption{
		network.WithBroadcastDelay(delayFn),
		network.WithUnicastDelay(delayFn),
		network.WithGossipJitter(cfg.GossipJitter),
		network.WithRandomSeed(seed),
	}
	if cfg.LogNetwork {
		opts = append(opts, network.WithNetworkLogger(network.DefaultNetworkLogger))
	}

	net := network.NewSimulatedNetwork(cfg.NetworkWorkers, opts...)
	defer net.Stop()

	peerMap := buildPeers(cfg.Topology, len(cfg.Powers))
	powerMap := make(map[int]int, len(cfg.Powers))
	for i, p := range cfg.Powers {
		powerMap[i] = p
	}

	nodes := make([]*consensus.Node, len(cfg.Powers))
	for i := range cfg.Powers {
		node := consensus.NewNode(i, cfg.Powers[i], powerMap)
		if cfg.Byzantine != nil {
			if b, ok := cfg.Byzantine[i]; ok {
				node.SetBehavior(&consensus.ByzantineBehavior{
					EquivocatePrevote:   b.EquivocatePrevote,
					EquivocatePrecommit: b.EquivocatePrecommit,
					ForceNilPrevote:     b.ForceNilPrevote,
					ForceNilPrecommit:   b.ForceNilPrecommit,
					SilentPrevote:       b.SilentPrevote,
					SilentPrecommit:     b.SilentPrecommit,
				})
				fmt.Printf("Node %d configured as Byzantine: %+v\n", i, *b)
			}
		}
		node.SetTimeouts(cfg.ProposalTimeout, cfg.PrevoteTimeout, cfg.PrecommitTimeout)
		node.SetMaxTimeout(cfg.MaxTimeout)
		node.SetNetwork(net, peerMap[i])
		nodes[i] = node
	}

	summaries := make([]*consensus.HeightSummary, 0, cfg.Heights)
	for height := 1; height <= cfg.Heights; height++ {
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

	if len(summaries) == 0 {
		return "", "", fmt.Errorf("no summaries generated")
	}

	baseName := time.Now().Format("20060102_150405")
	if cfg.Label != "" {
		baseName = fmt.Sprintf("%s_%s", baseName, sanitizeLabel(cfg.Label))
	}

	consensusPath, err := writeConsensusSummaryCSV(filepath.Join("summary", "current"), baseName, summaries)
	if err != nil {
		return "", "", err
	}
	if err := writeMetadataFile(filepath.Join("summary", "current"), baseName, cfg); err != nil {
		return "", "", err
	}

	timeoutPath, err := writeTimeoutSummaryCSV(filepath.Join("summary", "timeouts"), baseName, summaries)
	if err != nil {
		return "", "", err
	}
	if err := writeMetadataFile(filepath.Join("summary", "timeouts"), baseName, cfg); err != nil {
		return "", "", err
	}

	return consensusPath, timeoutPath, nil
}

func buildPeers(topology string, total int) map[int][]int {
	if topology == "full" {
		return buildFullPeers(total)
	}
	return buildRingPeers(total)
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

func buildFullPeers(total int) map[int][]int {
	if total <= 0 {
		return nil
	}
	peers := make(map[int][]int, total)
	for i := 0; i < total; i++ {
		list := make([]int, 0, total-1)
		for j := 0; j < total; j++ {
			if i == j {
				continue
			}
			list = append(list, j)
		}
		peers[i] = list
	}
	return peers
}

func writeConsensusSummaryCSV(dir, baseName string, summaries []*consensus.HeightSummary) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating summary directory: %w", err)
	}
	path := filepath.Join(dir, baseName+".csv")
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

func writeTimeoutSummaryCSV(dir, baseName string, summaries []*consensus.HeightSummary) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating timeout summary directory: %w", err)
	}
	path := filepath.Join(dir, baseName+".csv")
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating timeout summary file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"height",
		"proposal_timeouts",
		"prevote_timeouts",
		"precommit_timeouts",
		"total_timeouts",
	}
	if err := writer.Write(header); err != nil {
		return "", fmt.Errorf("writing timeout header: %w", err)
	}

	for _, s := range summaries {
		total := s.ProposalTimeouts + s.PrevoteTimeouts + s.PrecommitTimeouts
		record := []string{
			fmt.Sprintf("%d", s.Height),
			fmt.Sprintf("%d", s.ProposalTimeouts),
			fmt.Sprintf("%d", s.PrevoteTimeouts),
			fmt.Sprintf("%d", s.PrecommitTimeouts),
			fmt.Sprintf("%d", total),
		}
		if err := writer.Write(record); err != nil {
			return "", fmt.Errorf("writing timeout record for height %d: %w", s.Height, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("flushing timeout csv writer: %w", err)
	}

	return path, nil
}

func writeMetadataFile(dir, baseName string, cfg SimulationConfig) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating metadata directory: %w", err)
	}
	path := filepath.Join(dir, baseName+".txt")
	builder := &strings.Builder{}

	fmt.Fprintf(builder, "label=%s\n", cfg.Label)
	fmt.Fprintf(builder, "powers=%v\n", cfg.Powers)
	fmt.Fprintf(builder, "heights=%d\n", cfg.Heights)
	fmt.Fprintf(builder, "seed=%d\n", cfg.Seed)
	fmt.Fprintf(builder, "delay_ms=%d-%d\n", cfg.DelayMinMs, cfg.DelayMaxMs)
	fmt.Fprintf(builder, "gossip_jitter_ms=%.2f\n", cfg.GossipJitter.Seconds()*1000)
	fmt.Fprintf(builder, "proposal_timeout_ms=%d\n", cfg.ProposalTimeout.Milliseconds())
	fmt.Fprintf(builder, "prevote_timeout_ms=%d\n", cfg.PrevoteTimeout.Milliseconds())
	fmt.Fprintf(builder, "precommit_timeout_ms=%d\n", cfg.PrecommitTimeout.Milliseconds())
	fmt.Fprintf(builder, "max_timeout_ms=%d\n", cfg.MaxTimeout.Milliseconds())
	fmt.Fprintf(builder, "topology=%s\n", cfg.Topology)
	fmt.Fprintf(builder, "network_workers=%d\n", cfg.NetworkWorkers)
	fmt.Fprintf(builder, "log_network=%t\n", cfg.LogNetwork)

	if len(cfg.Byzantine) > 0 {
		fmt.Fprintln(builder, "byzantine_behaviours:")
		ids := make([]int, 0, len(cfg.Byzantine))
		for id := range cfg.Byzantine {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			b := cfg.Byzantine[id]
			fmt.Fprintf(builder, "  - node=%d equiv_prevote=%t equiv_precommit=%t force_nil_prevote=%t force_nil_precommit=%t silent_prevote=%t silent_precommit=%t\n",
				id,
				b.EquivocatePrevote,
				b.EquivocatePrecommit,
				b.ForceNilPrevote,
				b.ForceNilPrecommit,
				b.SilentPrevote,
				b.SilentPrecommit,
			)
		}
	}

	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func sanitizeLabel(label string) string {
	if label == "" {
		return "run"
	}
	label = strings.ToLower(label)
	label = strings.ReplaceAll(label, " ", "_")
	label = strings.ReplaceAll(label, string(filepath.Separator), "-")
	return label
}
