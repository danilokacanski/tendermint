package consensus

import (
	"fmt"
	"time"

	"Tendermint/internal/network"
	"Tendermint/internal/types"
)

type voteSet map[int]bool

type eventType int

const (
	eventProposalTimeout eventType = iota
	eventPrevoteTimeout
	eventPrecommitTimeout
)

type consensusEvent struct {
	height int
	round  int
	kind   eventType
}

type ByzantineBehavior struct {
	EquivocatePrevote   bool
	EquivocatePrecommit bool
	ForceNilPrevote     bool
	ForceNilPrecommit   bool
	SilentPrevote       bool
	SilentPrecommit     bool
}

type ValidatorState struct {
	LockedBlock  string
	LockedRound  int
	LockedHeight int
	ValidBlock   string
	ValidRound   int
	ValidHeight  int
	EvidenceLog  []*types.Evidence
	Jailed       bool
}

type Node struct {
	ID  int
	In  chan types.Message
	net network.Network

	total int

	currentHeight int
	currentRound  int
	roundActive   bool

	proposalReceived bool
	prevoteSent      bool
	precommitSent    bool

	prevoteCounts    map[int]map[string]voteSet
	precommitCounts  map[int]map[string]voteSet
	prevoteByVoter   map[int]map[int]string
	precommitByVoter map[int]map[int]string
	prevotePower     map[int]map[string]int
	precommitPower   map[int]map[string]int
	pendingMessages  []types.Message
	committed        bool
	committedBlock   string

	lastCommittedBlock  string
	lastCommittedHeight int

	state       *ValidatorState
	jailedPeers map[int]bool
	behavior    *ByzantineBehavior
	active      bool
	power       int
	powerMap    map[int]int
	totalPower  int
	quorumPower int

	internal chan consensusEvent

	proposalTimeout  time.Duration
	prevoteTimeout   time.Duration
	precommitTimeout time.Duration

	baseProposalTimeout  time.Duration
	basePrevoteTimeout   time.Duration
	basePrecommitTimeout time.Duration
	timeoutMultiplierCap int
	roundStart           int
	maxTimeout           time.Duration
	aborted              bool
}

func NewNode(id int, power int, powerMap map[int]int) *Node {
	if powerMap == nil {
		powerMap = make(map[int]int)
	}
	if _, ok := powerMap[id]; !ok {
		powerMap[id] = power
	}
	return &Node{
		ID:               id,
		In:               make(chan types.Message, 64),
		state:            &ValidatorState{},
		jailedPeers:      make(map[int]bool),
		power:            power,
		powerMap:         powerMap,
		proposalTimeout:  200 * time.Millisecond,
		prevoteTimeout:   200 * time.Millisecond,
		precommitTimeout: 200 * time.Millisecond,
		maxTimeout:       10 * time.Second,
	}
}

func (n *Node) SetBehavior(b *ByzantineBehavior) {
	n.behavior = b
}

func (n *Node) SetTimeouts(proposal, prevote, precommit time.Duration) {
	if proposal > 0 {
		n.proposalTimeout = proposal
		n.baseProposalTimeout = proposal
	}
	if prevote > 0 {
		n.prevoteTimeout = prevote
		n.basePrevoteTimeout = prevote
	}
	if precommit > 0 {
		n.precommitTimeout = precommit
		n.basePrecommitTimeout = precommit
	}
}

func (n *Node) SetMaxTimeout(d time.Duration) {
	if d > 0 {
		n.maxTimeout = d
	}
}

func (n *Node) SetNetwork(net network.Network) {
	n.net = net
	if n.In == nil {
		n.In = make(chan types.Message, 64)
	}
	net.Register(n.ID, n.In)
}

func (n *Node) SetPowerMap(powerMap map[int]int) {
	n.powerMap = powerMap
}

func (n *Node) RecomputeQuorum() {
	n.recomputeQuorum()
}

func (n *Node) QuorumPower() int {
	return n.quorumPower
}

func (n *Node) JailPeer(id int) {
	if n.jailedPeers == nil {
		n.jailedPeers = make(map[int]bool)
	}
	n.jailedPeers[id] = true
}

func (n *Node) Committed() bool {
	return n.committed
}

func (n *Node) CommittedBlock() string {
	return n.committedBlock
}

func (n *Node) Aborted() bool {
	return n.aborted
}

func (n *Node) Identifier() int {
	return n.ID
}

func (n *Node) broadcast(msg types.Message) {
	if n.net == nil {
		return
	}
	n.net.Broadcast(n.ID, msg)
}

func (n *Node) unicast(to int, msg types.Message) {
	if n.net == nil {
		return
	}
	n.net.Unicast(n.ID, to, msg)
}

func (n *Node) configureTimeouts(startRound int) {
	n.aborted = false
	if n.proposalTimeout == 0 {
		n.proposalTimeout = 200 * time.Millisecond
	}
	if n.prevoteTimeout == 0 {
		n.prevoteTimeout = 200 * time.Millisecond
	}
	if n.precommitTimeout == 0 {
		n.precommitTimeout = 200 * time.Millisecond
	}
	if n.baseProposalTimeout == 0 {
		n.baseProposalTimeout = n.proposalTimeout
	}
	if n.basePrevoteTimeout == 0 {
		n.basePrevoteTimeout = n.prevoteTimeout
	}
	if n.basePrecommitTimeout == 0 {
		n.basePrecommitTimeout = n.precommitTimeout
	}
	if n.timeoutMultiplierCap == 0 {
		n.timeoutMultiplierCap = 16
	}
	if n.maxTimeout == 0 {
		n.maxTimeout = 10 * time.Second
	}
	n.roundStart = startRound
}

func (n *Node) timeoutFor(kind eventType, round int) time.Duration {
	offset := round - n.roundStart
	if offset < 0 {
		offset = 0
	}
	multiplier := 1 << offset
	if n.timeoutMultiplierCap > 0 && multiplier > n.timeoutMultiplierCap {
		multiplier = n.timeoutMultiplierCap
	}
	if multiplier < 1 {
		multiplier = 1
	}

	var base time.Duration
	switch kind {
	case eventProposalTimeout:
		base = n.baseProposalTimeout
	case eventPrevoteTimeout:
		base = n.basePrevoteTimeout
	case eventPrecommitTimeout:
		base = n.basePrecommitTimeout
	default:
		base = 200 * time.Millisecond
	}
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	return time.Duration(multiplier) * base
}

func (n *Node) scheduleStageTimeout(kind eventType, height, round int) {
	if n.aborted {
		return
	}
	timeout := n.timeoutFor(kind, round)
	if n.maxTimeout > 0 && timeout > n.maxTimeout {
		n.abortConsensus(height, round, timeout)
		return
	}
	n.scheduleEvent(timeout, kind, height, round)
}

func (n *Node) escalateTimeoutCap() int {
	if n.timeoutMultiplierCap <= 0 {
		n.timeoutMultiplierCap = 16
		return n.timeoutMultiplierCap
	}
	if n.timeoutMultiplierCap < 512 {
		n.timeoutMultiplierCap *= 2
	}
	return n.timeoutMultiplierCap
}

func (n *Node) abortConsensus(height, round int, timeout time.Duration) {
	if n.aborted {
		return
	}
	n.aborted = true
	n.roundActive = false
	n.logf(types.ColorEvidence, "Aborting height %d at round %d: timeout %s exceeds limit %s", height, round, timeout, n.maxTimeout)
}

func (n *Node) scheduleEvent(after time.Duration, kind eventType, height, round int) {
	go func() {
		time.Sleep(after)
		n.internal <- consensusEvent{height: height, round: round, kind: kind}
	}()
}

func (n *Node) logf(color string, format string, args ...interface{}) {
	prefix := fmt.Sprintf("[H%d R%d][Node %d] ", n.currentHeight, n.currentRound, n.ID)
	fmt.Printf("%s%s%s%s\n", color, prefix, fmt.Sprintf(format, args...), types.ColorReset)
}

func (n *Node) conflictingVote(block string, valid bool, height, round int) (string, bool) {
	if block == "" || !valid {
		return fmt.Sprintf("Equiv_%d_%d_from_%d", height, round, n.ID), true
	}
	return "", false
}
