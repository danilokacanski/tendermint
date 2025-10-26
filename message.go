package main

type MessageType int

const (
	Proposal MessageType = iota
	Prevote
	Precommit
	Commit
	EvidenceMsg
)

const (
	colorReset     = "\033[0m"
	colorProposal  = "\033[36m" // cyan
	colorPrevote   = "\033[33m" // yellow
	colorPrecommit = "\033[35m" // magenta
	colorCommit    = "\033[32m" // green
	colorEvidence  = "\033[31m" // red
)

type Message struct {
	From     int
	Type     MessageType
	Height   int
	Round    int
	Block    string
	Valid    bool
	Evidence *Evidence
}

type Evidence struct {
	Height           int
	Round            int
	Stage            MessageType
	Offender         int
	ConflictingVotes []string
}

func (mt MessageType) Color() string {
	switch mt {
	case Proposal:
		return colorProposal
	case Prevote:
		return colorPrevote
	case Precommit:
		return colorPrecommit
	case Commit:
		return colorCommit
	case EvidenceMsg:
		return colorEvidence
	default:
		return colorReset
	}
}

func (mt MessageType) Label() string {
	switch mt {
	case Proposal:
		return "Proposal"
	case Prevote:
		return "Prevote"
	case Precommit:
		return "Precommit"
	case Commit:
		return "Commit"
	case EvidenceMsg:
		return "Evidence"
	default:
		return "Unknown"
	}
}
