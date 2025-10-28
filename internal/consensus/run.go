package consensus

import "sync"

// RunConsensus runs StartConsensus on every validator concurrently and returns
// a summary harvested from the first node that recorded metrics.
func RunConsensus(nodes []*Node, height int, startRound int) (*HeightSummary, bool) {
	var wg sync.WaitGroup
	results := make(chan bool, len(nodes))

	for _, n := range nodes {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()
			results <- node.StartConsensus(height, startRound, len(nodes))
		}(n)
	}

	wg.Wait()
	close(results)

	success := true
	for r := range results {
		if !r {
			success = false
		}
	}

	var summary *HeightSummary
	for _, n := range nodes {
		if m := n.MetricsForHeight(height); m != nil {
			summary = &HeightSummary{
				Height:            m.Height,
				Success:           m.Success,
				Rounds:            m.RoundsAttempted,
				CommitRound:       m.CommitRound,
				CommitBlock:       m.CommitBlock,
				CommitDuration:    m.CommitDuration,
				LockedRound:       m.LockedRound,
				LockedValue:       m.LockedValue,
				ValidRound:        m.ValidRound,
				ValidValue:        m.ValidValue,
				ProposalTimeouts:  m.ProposalTimeouts,
				PrevoteTimeouts:   m.PrevoteTimeouts,
				PrecommitTimeouts: m.PrecommitTimeouts,
			}
			if m.ProposerByRound != nil && m.CommitRound != 0 {
				if proposer, ok := m.ProposerByRound[m.CommitRound]; ok {
					summary.CommitProposer = proposer
				}
			}
			break
		}
	}

	return summary, success
}
