package consensus

import (
	"fmt"
	"time"

	"Tendermint/internal/types"
)

func (n *Node) handleMessage(msg types.Message) {
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
	case types.Proposal:
		n.handleProposal(msg, false)
	case types.Prevote:
		n.handlePrevote(msg)
	case types.Precommit:
		n.handlePrecommit(msg)
	case types.Commit:
		n.handleCommit(msg)
	case types.EvidenceMsg:
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

func (n *Node) handleProposal(msg types.Message, local bool) {
	if n.committed || msg.Round != n.currentRound || msg.Height != n.currentHeight {
		return
	}

	if local {
		n.logf(msg.Type.Color(), "Processing own proposal: %s (validRound=%d)", formatBlockForLog(msg.Block, msg.Valid), msg.ValidRound)
	} else {
		n.logf(msg.Type.Color(), "Received proposal from Node %d: %s (validRound=%d)", msg.From, formatBlockForLog(msg.Block, msg.Valid), msg.ValidRound)
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
	n.logf(types.ColorPrevote, "Proposal timeout, prevoting nil")
	n.sendPrevote(n.currentHeight, round, "", false, "timeout")
}

func (n *Node) sendPrevote(height, round int, block string, valid bool, reason string) {
	if n.prevoteSent {
		return
	}
	if n.state != nil && n.state.Jailed {
		n.logf(types.ColorPrevote, "Jailed validator; not sending prevote (%s)", reason)
		return
	}

	voteBlock := block
	voteValid := valid
	voteReason := reason

	if n.behavior != nil {
		if n.behavior.SilentPrevote {
			n.prevoteSent = true
			n.logf(types.ColorPrevote, "Byzantine behaviour: skipping prevote (%s)", reason)
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

	prevote := types.Message{
		From:   n.ID,
		Type:   types.Prevote,
		Height: height,
		Round:  round,
		Block:  voteBlock,
		Valid:  voteValid,
	}
	n.signMessage(&prevote)
	n.logf(prevote.Type.Color(), "Broadcasting prevote (%s): %s", voteReason, formatBlockForLog(prevote.Block, prevote.Valid))
	n.recordPrevote(prevote)
	n.broadcast(prevote)
	n.checkPrevoteQuorum(prevote.Height, prevote.Round, prevote.Block, prevote.Valid)

	if n.behavior != nil && n.behavior.EquivocatePrevote {
		conflictBlock, conflictValid := n.conflictingVote(voteBlock, voteValid, height, round)
		conflict := types.Message{
			From:   n.ID,
			Type:   types.Prevote,
			Height: height,
			Round:  round,
			Block:  conflictBlock,
			Valid:  conflictValid,
		}
		n.signMessage(&conflict)
		n.logf(conflict.Type.Color(), "Byzantine prevote (conflict): %s", formatBlockForLog(conflict.Block, conflict.Valid))
		n.broadcast(conflict)
	}

	n.scheduleStageTimeout(eventPrevoteTimeout, height, round)
}

func (n *Node) handlePrevote(msg types.Message) {
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
	n.logf(types.ColorPrecommit, "Prevote timeout, precommitting nil")
	n.sendPrecommit(n.currentHeight, round, "", false, "timeout")
}

func (n *Node) sendPrecommit(height, round int, block string, valid bool, reason string) {
	if n.precommitSent {
		return
	}
	if n.state != nil && n.state.Jailed {
		n.logf(types.ColorPrecommit, "Jailed validator; not sending precommit (%s)", reason)
		return
	}

	voteBlock := block
	voteValid := valid
	voteReason := reason

	if n.behavior != nil {
		if n.behavior.SilentPrecommit {
			n.precommitSent = true
			n.logf(types.ColorPrecommit, "Byzantine behaviour: skipping precommit (%s)", reason)
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

	precommit := types.Message{
		From:   n.ID,
		Type:   types.Precommit,
		Height: height,
		Round:  round,
		Block:  voteBlock,
		Valid:  voteValid,
	}
	n.signMessage(&precommit)
	n.logf(precommit.Type.Color(), "Broadcasting precommit (%s): %s", voteReason, formatBlockForLog(precommit.Block, precommit.Valid))
	n.recordPrecommit(precommit)
	n.broadcast(precommit)
	n.checkPrecommitQuorum(precommit.Height, precommit.Round, precommit.Block, precommit.Valid)

	if n.behavior != nil && n.behavior.EquivocatePrecommit {
		conflictBlock, conflictValid := n.conflictingVote(voteBlock, voteValid, height, round)
		conflict := types.Message{
			From:   n.ID,
			Type:   types.Precommit,
			Height: height,
			Round:  round,
			Block:  conflictBlock,
			Valid:  conflictValid,
		}
		n.signMessage(&conflict)
		n.logf(conflict.Type.Color(), "Byzantine precommit (conflict): %s", formatBlockForLog(conflict.Block, conflict.Valid))
		n.broadcast(conflict)
	}

	n.scheduleStageTimeout(eventPrecommitTimeout, height, round)
}

func (n *Node) handlePrecommit(msg types.Message) {
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
	n.logf(types.ColorCommit, "Precommit timeout, moving to next round")
	n.roundActive = false
}

func (n *Node) handleCommit(msg types.Message) {
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
		if n.activeMetrics != nil {
			if n.activeMetrics.CommitRound == 0 {
				n.activeMetrics.CommitRound = msg.Round
			}
			if n.activeMetrics.CommitBlock == "" {
				n.activeMetrics.CommitBlock = msg.Block
			}
			if n.activeMetrics.CommitTime.IsZero() {
				n.activeMetrics.CommitTime = time.Now()
				n.activeMetrics.CommitDuration = n.activeMetrics.CommitTime.Sub(n.activeMetrics.StartTime)
			}
			n.activeMetrics.Success = true
		}
		n.logf(msg.Type.Color(), "Commit observed from Node %d: %s ✅", msg.From, formatBlockForLog(msg.Block, msg.Valid))
	} else {
		n.logf(msg.Type.Color(), "Received nil commit from Node %d", msg.From)
	}
}

func (n *Node) handleEvidence(msg types.Message) {
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
		n.logf(types.ColorEvidence, "Node jailed due to evidence against itself")
	}
	n.recomputeQuorum()
}

func (n *Node) recordPrevote(msg types.Message) bool {
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
		n.reportEvidence(msg.Height, msg.Round, types.Prevote, msg.From, prev, key)
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

func (n *Node) recordPrecommit(msg types.Message) bool {
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
		n.reportEvidence(msg.Height, msg.Round, types.Precommit, msg.From, prev, key)
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
	n.logf(types.ColorCommit, "Committed block: %s ✅", block)

	if n.activeMetrics != nil {
		if n.activeMetrics.CommitRound == 0 {
			n.activeMetrics.CommitRound = round
		}
		if n.activeMetrics.CommitBlock == "" {
			n.activeMetrics.CommitBlock = block
		}
		if n.activeMetrics.CommitTime.IsZero() {
			n.activeMetrics.CommitTime = time.Now()
			n.activeMetrics.CommitDuration = n.activeMetrics.CommitTime.Sub(n.activeMetrics.StartTime)
		}
		n.activeMetrics.Success = true
	}

	commit := types.Message{
		From:   n.ID,
		Type:   types.Commit,
		Height: height,
		Round:  round,
		Block:  block,
		Valid:  true,
	}
	n.signMessage(&commit)
	n.broadcast(commit)
}

func (n *Node) reportEvidence(height, round int, stage types.MessageType, offender int, votes ...string) {
	evidence := &types.Evidence{
		Height:           height,
		Round:            round,
		Stage:            stage,
		Offender:         offender,
		ConflictingVotes: votes,
	}
	n.logf(types.ColorEvidence, "Detected double-sign evidence: offender=%d stage=%s votes=%v", offender, stage.Label(), votes)
	if n.state != nil {
		n.state.EvidenceLog = append(n.state.EvidenceLog, evidence)
	}
	msg := types.Message{
		From:     n.ID,
		Type:     types.EvidenceMsg,
		Height:   height,
		Round:    round,
		Evidence: evidence,
	}
	n.signMessage(&msg)
	n.broadcast(msg)
	if n.net != nil {
		n.net.ReportMisbehavior(n.ID, offender, "double-sign evidence")
	}
	n.jailedPeers[offender] = true
	if offender == n.ID && n.state != nil {
		n.state.Jailed = true
		n.logf(types.ColorEvidence, "Node jailed due to self-detected evidence")
	}
	n.recomputeQuorum()
}

func (n *Node) proposerFor(height, round int) int {
	ids, totalPower := n.activeValidatorIDs()
	if len(ids) == 0 {
		return n.ID
	}
	if totalPower <= 0 {
		return ids[0]
	}

	offset := (height + round) % totalPower
	for _, id := range ids {
		power := n.powerMap[id]
		if offset < power {
			return id
		}
		offset -= power
	}
	return ids[0]
}

func (n *Node) selectProposalBlock(height, round int) string {
	if n.state.ValidBlock != "" && n.state.ValidHeight == height {
		return n.state.ValidBlock
	}
	return fmt.Sprintf("Block_%d_%d", height, round)
}

func (n *Node) recomputeQuorum() {
	if n.powerMap == nil {
		n.powerMap = make(map[int]int)
		if n.power == 0 {
			n.power = 1
		}
		n.powerMap[n.ID] = n.power
	}
	_, activePower := n.activeValidatorIDs()
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

func blockKey(block string, valid bool) string {
	if !valid || block == "" {
		return "nil"
	}
	return block
}

func formatBlockForLog(block string, valid bool) string {
	if !valid || block == "" {
		return "nil"
	}
	return block
}
