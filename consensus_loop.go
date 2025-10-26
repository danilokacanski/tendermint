package main

func (n *Node) StartConsensus(height int, startRound int, total int) bool {
	if n.state == nil {
		n.state = &ValidatorState{}
	}
	if n.jailedPeers == nil {
		n.jailedPeers = make(map[int]bool)
	}
	if n.jailedPeers[n.ID] {
		n.state.Jailed = true
	}
	n.total = total
	n.recomputeQuorum()
	n.currentHeight = height
	n.currentRound = startRound
	n.roundActive = true
	n.proposalReceived = false
	n.prevoteSent = false
	n.precommitSent = false
	n.committed = false
	n.committedBlock = ""
	n.active = true
	defer func() { n.active = false }()

	if n.state.Jailed {
		n.logf(EvidenceMsg.Color(), "Validator jailed; skipping height %d", height)
		return true
	}

	if n.state.LockedHeight != height {
		n.state.LockedBlock = ""
		n.state.LockedRound = 0
		n.state.LockedHeight = height
	}
	if n.state.ValidHeight != height {
		n.state.ValidBlock = ""
		n.state.ValidRound = 0
		n.state.ValidHeight = height
	}

	n.prevoteCounts = make(map[int]map[string]voteSet)
	n.precommitCounts = make(map[int]map[string]voteSet)
	n.prevoteByVoter = make(map[int]map[int]string)
	n.precommitByVoter = make(map[int]map[int]string)
	n.prevotePower = make(map[int]map[string]int)
	n.precommitPower = make(map[int]map[string]int)
	n.pendingMessages = n.pendingMessages[:0]

	if n.internal == nil {
		n.internal = make(chan consensusEvent, 32)
	}
	n.configureTimeouts(startRound)

	n.clearOldMessages(height)

	round := startRound
	roundsTried := 0
	nextEscalation := 0
	for !n.committed && !n.aborted {
		n.runRound(height, round)
		roundsTried++
		if n.committed || n.aborted {
			break
		}
		round++
		if roundsTried >= nextEscalation && !n.aborted {
			newCap := n.escalateTimeoutCap()
			n.logf(Prevote.Color(), "Escalating timeouts after %d rounds without quorum (cap=%dx)", roundsTried, newCap)
			nextEscalation = roundsTried * 2
		}
	}

	n.lastCommittedBlock = n.committedBlock
	n.lastCommittedHeight = height
	return n.committed
}

func (n *Node) clearOldMessages(height int) {
Loop:
	for {
		select {
		case msg := <-n.In:
			if msg.Height < height {
				continue
			}
			n.pendingMessages = append(n.pendingMessages, msg)
		default:
			break Loop
		}
	}
}

func (n *Node) runRound(height int, round int) {
	n.currentRound = round
	n.roundActive = true
	n.proposalReceived = false
	n.prevoteSent = false
	n.precommitSent = false

	n.scheduleStageTimeout(eventProposalTimeout, height, round)
	if n.aborted {
		return
	}

	if n.ID == n.proposerFor(height, round) {
		blockID := n.selectProposalBlock(height, round)
		proposal := Message{
			From:   n.ID,
			Type:   Proposal,
			Height: height,
			Round:  round,
			Block:  blockID,
			Valid:  blockID != "",
		}
		n.logf(Proposal.Color(), "Proposed block: %s", formatBlockForLog(proposal.Block, proposal.Valid))
		n.broadcast(proposal)
		n.handleProposal(proposal, true)
	}

	for n.roundActive && !n.committed {
		if len(n.pendingMessages) > 0 {
			msg := n.pendingMessages[0]
			n.pendingMessages = n.pendingMessages[1:]
			n.handleMessage(msg)
			continue
		}

		select {
		case msg := <-n.In:
			n.handleMessage(msg)
		case ev := <-n.internal:
			if ev.height != height || ev.round != n.currentRound {
				continue
			}
			n.handleEvent(ev)
		}
	}
}
