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

// ByzantineConfig describes optional faulty behaviours a validator can exhibit
// during the simulation. Fields map 1:1 to internal/consensus.ByzantineBehavior.
type ByzantineConfig struct {
	EquivocatePrevote   bool
	EquivocatePrecommit bool
	ForceNilPrevote     bool
	ForceNilPrecommit   bool
	SilentPrevote       bool
	SilentPrecommit     bool
}

// SimulationConfig captures a single simulator configuration. Most fields have
// sensible defaults so callers can override only what they need.
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

// SimulationGrid lists per-dimension option sets; RunSimulationGrid will take
// the Cartesian product of the supplied values and execute every scenario.
type SimulationGrid struct {
	Labels            []string
	PowerSets         [][]int
	Heights           []int
	Seeds             []int64
	DelayMinMs        []int
	DelayMaxMs        []int
	GossipJitters     []time.Duration
	ProposalTimeouts  []time.Duration
	PrevoteTimeouts   []time.Duration
	PrecommitTimeouts []time.Duration
	MaxTimeouts       []time.Duration
	Topologies        []string
	NetworkWorkers    []int
	LogNetworkOptions []bool
	ByzantineVariants []map[int]*ByzantineConfig
}

// RunArtifact records the result of a single simulation run together with the
// exact configuration and generated CSV paths.
type RunArtifact struct {
	Config           SimulationConfig
	ConsensusSummary string
	TimeoutSummary   string
}

// RunSimulation executes one simulator run using the supplied configuration and
// returns the paths to the consensus and timeout CSV reports.
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
		if summary == nil {
			summary = &consensus.HeightSummary{Height: height, Success: success}
		}
		summary.Height = height
		summary.Success = success && summary.Success
		if !summary.Success {
			summary.CommitBlock = ""
			summary.CommitDuration = 0
			summary.CommitProposer = 0
		}
		summaries = append(summaries, summary)
		if !summary.Success {
			fmt.Printf("[Height %d] Aborted: validators exceeded max consensus timeout\n", height)
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

// RunSimulationGrid iterates across all combinations defined in the grid (using
// the base config as defaults) and calls RunSimulation for each combination.
func RunSimulationGrid(base SimulationConfig, grid SimulationGrid) ([]RunArtifact, error) {
	choices := buildGridChoices(grid)
	if len(choices) == 0 {
		cfgCopy := base
		if cfgCopy.Label == "" {
			cfgCopy.Label = "run"
		}
		cons, timeouts, err := RunSimulation(cfgCopy)
		if err != nil {
			return nil, err
		}
		return []RunArtifact{{Config: cfgCopy, ConsensusSummary: cons, TimeoutSummary: timeouts}}, nil
	}
	var results []RunArtifact
	var dfs func(idx int, cfg SimulationConfig, labelParts []string) error
	dfs = func(idx int, cfg SimulationConfig, labelParts []string) error {
		if idx == len(choices) {
			cfgCopy := cfg
			cfgCopy.Label = combineLabel(base.Label, labelParts)
			cons, timeouts, err := RunSimulation(cfgCopy)
			if err != nil {
				return err
			}
			results = append(results, RunArtifact{Config: cfgCopy, ConsensusSummary: cons, TimeoutSummary: timeouts})
			return nil
		}
		for _, option := range choices[idx] {
			cfgNext := cfg
			option.apply(&cfgNext)
			nextParts := labelParts
			if option.label != "" {
				nextParts = append(append([]string(nil), labelParts...), option.label)
			}
			if err := dfs(idx+1, cfgNext, nextParts); err != nil {
				return err
			}
		}
		return nil
	}
	if err := dfs(0, base, nil); err != nil {
		return nil, err
	}
	return results, nil
}

// gridChoice is a helper that ties a descriptive label and an apply function so
// SimulationGrid dimensions can be processed uniformly.
type gridChoice struct {
	label string
	apply func(*SimulationConfig)
}

// buildGridChoices converts every populated SimulationGrid field into a list of
// options per dimension for later Cartesian-product traversal.
func buildGridChoices(grid SimulationGrid) [][]gridChoice {
	var fields [][]gridChoice
	if len(grid.Labels) > 0 {
		choices := make([]gridChoice, len(grid.Labels))
		for i, lbl := range grid.Labels {
			label := sanitizeLabel(lbl)
			choices[i] = gridChoice{
				label: label,
				apply: func(cfg *SimulationConfig) {
					cfg.Label = lbl
				},
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.PowerSets) > 0 {
		choices := make([]gridChoice, len(grid.PowerSets))
		for i, powers := range grid.PowerSets {
			powersCopy := append([]int(nil), powers...)
			pc := powersCopy
			label := fmt.Sprintf("powers=%s", formatIntSlice(pc))
			choices[i] = gridChoice{
				label: label,
				apply: func(cfg *SimulationConfig) {
					cfg.Powers = append([]int(nil), pc...)
				},
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.Heights) > 0 {
		choices := make([]gridChoice, len(grid.Heights))
		for i, h := range grid.Heights {
			hVal := h
			choices[i] = gridChoice{
				label: fmt.Sprintf("heights=%d", hVal),
				apply: func(cfg *SimulationConfig) { cfg.Heights = hVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.Seeds) > 0 {
		choices := make([]gridChoice, len(grid.Seeds))
		for i, s := range grid.Seeds {
			sVal := s
			choices[i] = gridChoice{
				label: fmt.Sprintf("seed=%d", sVal),
				apply: func(cfg *SimulationConfig) { cfg.Seed = sVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.DelayMinMs) > 0 {
		choices := make([]gridChoice, len(grid.DelayMinMs))
		for i, d := range grid.DelayMinMs {
			dVal := d
			choices[i] = gridChoice{
				label: fmt.Sprintf("delaymin=%d", dVal),
				apply: func(cfg *SimulationConfig) { cfg.DelayMinMs = dVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.DelayMaxMs) > 0 {
		choices := make([]gridChoice, len(grid.DelayMaxMs))
		for i, d := range grid.DelayMaxMs {
			dVal := d
			choices[i] = gridChoice{
				label: fmt.Sprintf("delaymax=%d", dVal),
				apply: func(cfg *SimulationConfig) { cfg.DelayMaxMs = dVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.GossipJitters) > 0 {
		choices := make([]gridChoice, len(grid.GossipJitters))
		for i, j := range grid.GossipJitters {
			jVal := j
			choices[i] = gridChoice{
				label: fmt.Sprintf("jitter=%dms", jVal.Milliseconds()),
				apply: func(cfg *SimulationConfig) { cfg.GossipJitter = jVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.ProposalTimeouts) > 0 {
		choices := make([]gridChoice, len(grid.ProposalTimeouts))
		for i, t := range grid.ProposalTimeouts {
			tVal := t
			choices[i] = gridChoice{
				label: fmt.Sprintf("proposal=%dms", tVal.Milliseconds()),
				apply: func(cfg *SimulationConfig) { cfg.ProposalTimeout = tVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.PrevoteTimeouts) > 0 {
		choices := make([]gridChoice, len(grid.PrevoteTimeouts))
		for i, t := range grid.PrevoteTimeouts {
			tVal := t
			choices[i] = gridChoice{
				label: fmt.Sprintf("prevote=%dms", tVal.Milliseconds()),
				apply: func(cfg *SimulationConfig) { cfg.PrevoteTimeout = tVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.PrecommitTimeouts) > 0 {
		choices := make([]gridChoice, len(grid.PrecommitTimeouts))
		for i, t := range grid.PrecommitTimeouts {
			tVal := t
			choices[i] = gridChoice{
				label: fmt.Sprintf("precommit=%dms", tVal.Milliseconds()),
				apply: func(cfg *SimulationConfig) { cfg.PrecommitTimeout = tVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.MaxTimeouts) > 0 {
		choices := make([]gridChoice, len(grid.MaxTimeouts))
		for i, t := range grid.MaxTimeouts {
			tVal := t
			choices[i] = gridChoice{
				label: fmt.Sprintf("maxtimeout=%dms", tVal.Milliseconds()),
				apply: func(cfg *SimulationConfig) { cfg.MaxTimeout = tVal },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.Topologies) > 0 {
		choices := make([]gridChoice, len(grid.Topologies))
		for i, topo := range grid.Topologies {
			topology := topo
			choices[i] = gridChoice{
				label: fmt.Sprintf("topo=%s", sanitizeLabel(topology)),
				apply: func(cfg *SimulationConfig) { cfg.Topology = topology },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.NetworkWorkers) > 0 {
		choices := make([]gridChoice, len(grid.NetworkWorkers))
		for i, workers := range grid.NetworkWorkers {
			w := workers
			choices[i] = gridChoice{
				label: fmt.Sprintf("workers=%d", w),
				apply: func(cfg *SimulationConfig) { cfg.NetworkWorkers = w },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.LogNetworkOptions) > 0 {
		choices := make([]gridChoice, len(grid.LogNetworkOptions))
		for i, option := range grid.LogNetworkOptions {
			op := option
			choices[i] = gridChoice{
				label: fmt.Sprintf("lognet=%t", op),
				apply: func(cfg *SimulationConfig) { cfg.LogNetwork = op },
			}
		}
		fields = append(fields, choices)
	}
	if len(grid.ByzantineVariants) > 0 {
		choices := make([]gridChoice, len(grid.ByzantineVariants))
		for i, variant := range grid.ByzantineVariants {
			variantCopy := cloneByzantineMap(variant)
			label := fmt.Sprintf("byz=%d", len(variantCopy))
			choices[i] = gridChoice{
				label: label,
				apply: func(cfg *SimulationConfig) { cfg.Byzantine = cloneByzantineMap(variantCopy) },
			}
		}
		fields = append(fields, choices)
	}
	return fields
}

// combineLabel merges the base label and dimension descriptors into a
// filesystem-safe identifier used when naming CSV/metadata outputs.
func combineLabel(base string, parts []string) string {
	var segments []string
	if base != "" {
		segments = append(segments, sanitizeLabel(base))
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		segments = append(segments, sanitizeLabel(part))
	}
	if len(segments) == 0 {
		return "run"
	}
	return strings.Join(segments, "_")
}

// formatIntSlice renders the voting-power distribution as a short string (e.g.
// 3-2-1-1) to keep labels human readable.
func formatIntSlice(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, "-")
}

// cloneByzantineMap creates a deep copy of the byzantine configuration map so
// every run can mutate its own copy without affecting others.
func cloneByzantineMap(src map[int]*ByzantineConfig) map[int]*ByzantineConfig {
	if src == nil {
		return nil
	}
	clone := make(map[int]*ByzantineConfig, len(src))
	for id, cfg := range src {
		if cfg == nil {
			clone[id] = nil
			continue
		}
		copyCfg := *cfg
		clone[id] = &copyCfg
	}
	return clone
}

// buildPeers picks the appropriate peer topology builder.
func buildPeers(topology string, total int) map[int][]int {
	if topology == "full" {
		return buildFullPeers(total)
	}
	return buildRingPeers(total)
}

// buildRingPeers returns a bidirectional ring (each validator connected to its
// immediate neighbours).
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

// buildFullPeers connects every validator with every other validator.
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

// writeConsensusSummaryCSV dumps the per-height consensus metrics to disk and
// returns the resulting path.
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

// writeTimeoutSummaryCSV dumps the timeout counters per height to disk and
// returns the resulting path.
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

// writeMetadataFile persists the full configuration next to the generated CSV
// so downstream tooling knows exactly which parameters produced the run.
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
