package main

import (
	"fmt"
	"time"
)

func (n *Node) handleMessage(msg Message) {
	if msg.Height < n.currentHeight {
		return
	}
	if msg.Height > n.currentHeight {
		n.pendingMessages = append(n.pendingMessages, msg)
		n.roundActive = false
		return
	}

	if msg.Round < n.currentRound {
		return
	}
	if msg.Round > n.currentRound {
		n.pendingMessages = append(n.pendingMessages, msg)
		n.roundActive = false
		return
	}

	switch msg.Type {
	case Proposal:
		n.handleProposal(msg, false)
	case Prevote:
		n.handlePrevote(msg)
	case Precommit:
		n.handlePrecommit(msg)
	case Commit:
		n.handleCommit(msg)
	case EvidenceMsg:
		n.handleEvidence(msg)
	}
}

func (n *Node) handleEvent(ev consensusEvent) {
	switch ev.kind {
	case eventProposalTimeout:
		n.onProposalTimeout(ev.round)
	case eventPrevoteTimeout:
		n.onPrevoteTimeout(ev.round)
	case eventPrecommitTimeout:
		n.onPrecommitTimeout(ev.round)
	}
}

func (n *Node) handleProposal(msg Message, local bool) {
	if n.committed || msg.Round != n.currentRound || msg.Height != n.currentHeight {
		return
	}

	if local {
		n.logf(msg.Type.Color(), "Processing own proposal: %s", formatBlockForLog(msg.Block, msg.Valid))
	} else {
		n.logf(msg.Type.Color(), "Received proposal from Node %d: %s", msg.From, formatBlockForLog(msg.Block, msg.Valid))
	}

	n.proposalReceived = true

	if n.prevoteSent {
		return
	}

	voteValid := msg.Valid
	voteBlock := msg.Block

	if msg.Valid && n.state.LockedBlock != "" && (n.state.LockedBlock != msg.Block || n.state.LockedHeight != msg.Height) {
		voteValid = false
		voteBlock = ""
	}

	n.sendPrevote(msg.Height, msg.Round, voteBlock, voteValid, "proposal")
}

func (n *Node) onProposalTimeout(round int) {
	if n.committed || round != n.currentRound || n.prevoteSent {
		return
	}
	n.logf(Prevote.Color(), "Proposal timeout, prevoting nil")
	n.sendPrevote(n.currentHeight, round, "", false, "timeout")
}

func (n *Node) sendPrevote(height, round int, block string, valid bool, reason string) {
	if n.prevoteSent {
		return
	}
	if n.state != nil && n.state.Jailed {
		n.logf(Prevote.Color(), "Jailed validator; not sending prevote (%s)", reason)
		return
	}

	voteBlock := block
	voteValid := valid
	voteReason := reason

	if n.behavior != nil {
		if n.behavior.SilentPrevote {
			n.prevoteSent = true
			n.logf(Prevote.Color(), "Byzantine behaviour: skipping prevote (%s)", reason)
			n.scheduleStageTimeout(eventPrevoteTimeout, height, round)
			return
		}
		if n.behavior.ForceNilPrevote {
			voteBlock = ""
			voteValid = false
			voteReason = reason + "|force-nil"
		}
	}

	n.prevoteSent = true

	prevote := Message{
		From:   n.ID,
		Type:   Prevote,
		Height: height,
		Round:  round,
		Block:  voteBlock,
		Valid:  voteValid,
	}
	n.logf(prevote.Type.Color(), "Broadcasting prevote (%s): %s", voteReason, formatBlockForLog(prevote.Block, prevote.Valid))
	n.recordPrevote(prevote)
	n.broadcast(prevote)
	n.checkPrevoteQuorum(prevote.Height, prevote.Round, prevote.Block, prevote.Valid)

	if n.behavior != nil && n.behavior.EquivocatePrevote {
		conflictBlock, conflictValid := n.conflictingVote(voteBlock, voteValid, height, round)
		conflict := Message{
			From:   n.ID,
			Type:   Prevote,
			Height: height,
			Round:  round,
			Block:  conflictBlock,
			Valid:  conflictValid,
		}
		n.logf(conflict.Type.Color(), "Byzantine prevote (conflict): %s", formatBlockForLog(conflict.Block, conflict.Valid))
		n.broadcast(conflict)
	}

	n.scheduleStageTimeout(eventPrevoteTimeout, height, round)
}

func (n *Node) handlePrevote(msg Message) {
	if n.committed || msg.Round != n.currentRound || msg.Height != n.currentHeight {
		return
	}
	if n.recordPrevote(msg) {
		n.logf(msg.Type.Color(), "Prevote received from Node %d for %s", msg.From, formatBlockForLog(msg.Block, msg.Valid))
		n.checkPrevoteQuorum(msg.Height, msg.Round, msg.Block, msg.Valid)
	}
}

func (n *Node) onPrevoteTimeout(round int) {
	if n.committed || round != n.currentRound || n.precommitSent {
		return
	}
	n.logf(Precommit.Color(), "Prevote timeout, precommitting nil")
	n.sendPrecommit(n.currentHeight, round, "", false, "timeout")
}

func (n *Node) sendPrecommit(height, round int, block string, valid bool, reason string) {
	if n.precommitSent {
		return
	}
	if n.state != nil && n.state.Jailed {
		n.logf(Precommit.Color(), "Jailed validator; not sending precommit (%s)", reason)
		return
	}

	voteBlock := block
	voteValid := valid
	voteReason := reason

	if n.behavior != nil {
		if n.behavior.SilentPrecommit {
			n.precommitSent = true
			n.logf(Precommit.Color(), "Byzantine behaviour: skipping precommit (%s)", reason)
			n.scheduleStageTimeout(eventPrecommitTimeout, height, round)
			return
		}
		if n.behavior.ForceNilPrecommit {
			voteBlock = ""
			voteValid = false
			voteReason = reason + "|force-nil"
		}
	}

	n.precommitSent = true

	precommit := Message{
		From:   n.ID,
		Type:   Precommit,
		Height: height,
		Round:  round,
		Block:  voteBlock,
		Valid:  voteValid,
	}
	n.logf(precommit.Type.Color(), "Broadcasting precommit (%s): %s", voteReason, formatBlockForLog(precommit.Block, precommit.Valid))
	n.recordPrecommit(precommit)
	n.broadcast(precommit)
	n.checkPrecommitQuorum(precommit.Height, precommit.Round, precommit.Block, precommit.Valid)

	if n.behavior != nil && n.behavior.EquivocatePrecommit {
		conflictBlock, conflictValid := n.conflictingVote(voteBlock, voteValid, height, round)
		conflict := Message{
			From:   n.ID,
			Type:   Precommit,
			Height: height,
			Round:  round,
			Block:  conflictBlock,
			Valid:  conflictValid,
		}
		n.logf(conflict.Type.Color(), "Byzantine precommit (conflict): %s", formatBlockForLog(conflict.Block, conflict.Valid))
		n.broadcast(conflict)
	}

	n.scheduleStageTimeout(eventPrecommitTimeout, height, round)
}

func (n *Node) handlePrecommit(msg Message) {
	if n.committed || msg.Round != n.currentRound || msg.Height != n.currentHeight {
		return
	}
	if n.recordPrecommit(msg) {
		n.logf(msg.Type.Color(), "Precommit received from Node %d for %s", msg.From, formatBlockForLog(msg.Block, msg.Valid))
		n.checkPrecommitQuorum(msg.Height, msg.Round, msg.Block, msg.Valid)
	}
}

func (n *Node) onPrecommitTimeout(round int) {
	if n.committed || round != n.currentRound {
		return
	}
	n.logf(Commit.Color(), "Precommit timeout, moving to next round")
	n.roundActive = false
}

func (n *Node) handleCommit(msg Message) {
	if msg.Height != n.currentHeight {
		if msg.Height > n.currentHeight {
			n.pendingMessages = append(n.pendingMessages, msg)
			n.roundActive = false
		}
		return
	}

	if msg.Valid {
		n.committed = true
		n.roundActive = false
		n.committedBlock = msg.Block
		n.logf(msg.Type.Color(), "Commit observed from Node %d: %s ✅", msg.From, formatBlockForLog(msg.Block, msg.Valid))
	} else {
		n.logf(msg.Type.Color(), "Received nil commit from Node %d", msg.From)
	}
}

func (n *Node) handleEvidence(msg Message) {
	if msg.Evidence == nil {
		return
	}
	ev := msg.Evidence
	n.logf(msg.Type.Color(), "Evidence received from Node %d: offender=%d stage=%s votes=%v", msg.From, ev.Offender, ev.Stage.Label(), ev.ConflictingVotes)
	if n.state != nil {
		n.state.EvidenceLog = append(n.state.EvidenceLog, ev)
	}
	n.jailedPeers[ev.Offender] = true
	if ev.Offender == n.ID && n.state != nil {
		n.state.Jailed = true
		n.logf(EvidenceMsg.Color(), "Node jailed due to evidence against itself")
	}
	n.recomputeQuorum()
}

func (n *Node) recordPrevote(msg Message) bool {
	if n.jailedPeers != nil && n.jailedPeers[msg.From] {
		return false
	}
	if _, ok := n.prevoteCounts[msg.Round]; !ok {
		n.prevoteCounts[msg.Round] = make(map[string]voteSet)
	}
	if _, ok := n.prevoteByVoter[msg.Round]; !ok {
		n.prevoteByVoter[msg.Round] = make(map[int]string)
	}

	key := blockKey(msg.Block, msg.Valid)
	if prev, ok := n.prevoteByVoter[msg.Round][msg.From]; ok && prev != key {
		n.reportEvidence(msg.Height, msg.Round, Prevote, msg.From, prev, key)
		return false
	}
	n.prevoteByVoter[msg.Round][msg.From] = key

	if _, ok := n.prevoteCounts[msg.Round][key]; !ok {
		n.prevoteCounts[msg.Round][key] = make(voteSet)
	}
	if n.prevoteCounts[msg.Round][key][msg.From] {
		return false
	}
	n.prevoteCounts[msg.Round][key][msg.From] = true
	if _, ok := n.prevotePower[msg.Round]; !ok {
		n.prevotePower[msg.Round] = make(map[string]int)
	}
	n.prevotePower[msg.Round][key] += n.voterPower(msg.From)
	return true
}

func (n *Node) recordPrecommit(msg Message) bool {
	if n.jailedPeers != nil && n.jailedPeers[msg.From] {
		return false
	}
	if _, ok := n.precommitCounts[msg.Round]; !ok {
		n.precommitCounts[msg.Round] = make(map[string]voteSet)
	}
	if _, ok := n.precommitByVoter[msg.Round]; !ok {
		n.precommitByVoter[msg.Round] = make(map[int]string)
	}

	key := blockKey(msg.Block, msg.Valid)
	if prev, ok := n.precommitByVoter[msg.Round][msg.From]; ok && prev != key {
		n.reportEvidence(msg.Height, msg.Round, Precommit, msg.From, prev, key)
		return false
	}
	n.precommitByVoter[msg.Round][msg.From] = key

	if _, ok := n.precommitCounts[msg.Round][key]; !ok {
		n.precommitCounts[msg.Round][key] = make(voteSet)
	}
	if n.precommitCounts[msg.Round][key][msg.From] {
		return false
	}
	n.precommitCounts[msg.Round][key][msg.From] = true
	if _, ok := n.precommitPower[msg.Round]; !ok {
		n.precommitPower[msg.Round] = make(map[string]int)
	}
	n.precommitPower[msg.Round][key] += n.voterPower(msg.From)
	return true
}

func (n *Node) checkPrevoteQuorum(height, round int, block string, valid bool) {
	key := blockKey(block, valid)
	roundPower, ok := n.prevotePower[round]
	if !ok {
		return
	}
	count := roundPower[key]

	if count < n.quorumPower {
		return
	}

	if !valid {
		if round >= n.state.LockedRound && height >= n.state.LockedHeight {
			n.state.LockedBlock = ""
			n.state.LockedRound = 0
			n.state.LockedHeight = 0
		}
		if !n.precommitSent {
			n.sendPrecommit(height, round, "", false, "nil-quorum")
		}
		return
	}

	if n.state.LockedBlock != block || n.state.LockedRound < round || n.state.LockedHeight != height {
		n.state.LockedBlock = block
		n.state.LockedRound = round
		n.state.LockedHeight = height
	}
	n.state.ValidBlock = block
	n.state.ValidRound = round
	n.state.ValidHeight = height

	if !n.precommitSent {
		n.sendPrecommit(height, round, block, true, "quorum")
	}
}

func (n *Node) checkPrecommitQuorum(height, round int, block string, valid bool) {
	key := blockKey(block, valid)
	roundPower, ok := n.precommitPower[round]
	if !ok {
		return
	}
	count := roundPower[key]

	if count < n.quorumPower || n.committed {
		return
	}

	if !valid {
		n.roundActive = false
		return
	}

	n.committed = true
	n.roundActive = false
	n.committedBlock = block
	n.logf(Commit.Color(), "Committed block: %s ✅", block)

	commit := Message{
		From:   n.ID,
		Type:   Commit,
		Height: height,
		Round:  round,
		Block:  block,
		Valid:  true,
	}
	n.broadcast(commit)
}

func (n *Node) reportEvidence(height, round int, stage MessageType, offender int, votes ...string) {
	evidence := &Evidence{
		Height:           height,
		Round:            round,
		Stage:            stage,
		Offender:         offender,
		ConflictingVotes: votes,
	}
	n.logf(EvidenceMsg.Color(), "Detected double-sign evidence: offender=%d stage=%s votes=%v", offender, stage.Label(), votes)
	if n.state != nil {
		n.state.EvidenceLog = append(n.state.EvidenceLog, evidence)
	}
	msg := Message{
		From:     n.ID,
		Type:     EvidenceMsg,
		Height:   height,
		Round:    round,
		Evidence: evidence,
	}
	n.broadcast(msg)
	n.jailedPeers[offender] = true
	if offender == n.ID && n.state != nil {
		n.state.Jailed = true
		n.logf(EvidenceMsg.Color(), "Node jailed due to self-detected evidence")
	}
	n.recomputeQuorum()
}

func (n *Node) proposerFor(height, round int) int {
	start := (height + round) % n.total
	total := n.total
	for i := 0; i < total; i++ {
		candidate := (start + i) % total
		if n.jailedPeers != nil && n.jailedPeers[candidate] {
			continue
		}
		return candidate
	}
	return start
}

func (n *Node) selectProposalBlock(height, round int) string {
	if n.state.ValidBlock != "" && n.state.ValidHeight == height {
		return n.state.ValidBlock
	}
	return fmt.Sprintf("Block_%d_%d", height, round)
}

func (n *Node) scheduleEvent(after time.Duration, kind eventType, height, round int) {
	go func() {
		time.Sleep(after)
		n.internal <- consensusEvent{height: height, round: round, kind: kind}
	}()
}

func (n *Node) logf(color string, format string, args ...interface{}) {
	prefix := fmt.Sprintf("[H%d R%d][Node %d] ", n.currentHeight, n.currentRound, n.ID)
	fmt.Printf("%s%s%s%s\n", color, prefix, fmt.Sprintf(format, args...), colorReset)
}

func (n *Node) conflictingVote(block string, valid bool, height, round int) (string, bool) {
	if block == "" || !valid {
		return fmt.Sprintf("Equiv_%d_%d_from_%d", height, round, n.ID), true
	}
	return "", false
}

func (n *Node) recomputeQuorum() {
	if n.powerMap == nil {
		n.powerMap = make(map[int]int)
		if n.power == 0 {
			n.power = 1
		}
		n.powerMap[n.ID] = n.power
	}
	activePower := 0
	for id, power := range n.powerMap {
		if power <= 0 {
			continue
		}
		jailed := n.jailedPeers != nil && n.jailedPeers[id]
		if id == n.ID && n.state != nil && n.state.Jailed {
			jailed = true
		}
		if jailed {
			continue
		}
		activePower += power
	}
	if activePower <= 0 {
		activePower = n.power
	}
	quorum := (2*activePower + 2) / 3
	if quorum > activePower {
		quorum = activePower
	}
	if quorum < 1 {
		quorum = 1
	}
	n.totalPower = activePower
	n.quorumPower = quorum
}

func (n *Node) voterPower(id int) int {
	if n.powerMap != nil {
		if p, ok := n.powerMap[id]; ok {
			return p
		}
	}
	return 1
}
