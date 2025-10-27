package consensus

import "time"

// HeightMetrics captures detailed per-height statistics for an individual validator.
type HeightMetrics struct {
	Height            int
	RoundsAttempted   int
	CommitRound       int
	CommitBlock       string
	CommitDuration    time.Duration
	StartTime         time.Time
	CommitTime        time.Time
	ProposerByRound   map[int]int
	LockedRound       int
	LockedValue       string
	ValidRound        int
	ValidValue        string
	Success           bool
	Aborted           bool
	AbortReason       string
	ProposalTimeouts  int
	PrevoteTimeouts   int
	PrecommitTimeouts int
}

// HeightSummary is an aggregated, human-friendly view of the metrics that
// RunConsensus returns to callers.
type HeightSummary struct {
	Height            int
	Success           bool
	Rounds            int
	CommitRound       int
	CommitBlock       string
	CommitDuration    time.Duration
	CommitProposer    int
	LockedRound       int
	LockedValue       string
	ValidRound        int
	ValidValue        string
	ProposalTimeouts  int
	PrevoteTimeouts   int
	PrecommitTimeouts int
}
