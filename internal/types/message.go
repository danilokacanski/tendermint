package types

import "fmt"

type MessageType int

const (
	Proposal MessageType = iota
	Prevote
	Precommit
	Commit
	EvidenceMsg
)

const (
	ColorReset     = "\033[0m"
	ColorProposal  = "\033[36m"
	ColorPrevote   = "\033[33m"
	ColorPrecommit = "\033[35m"
	ColorCommit    = "\033[32m"
	ColorEvidence  = "\033[31m"
)

type Message struct {
	From       int
	Type       MessageType
	Height     int
	Round      int
	Block      string
	Valid      bool
	ValidRound int
	Signature  []byte
	Evidence   *Evidence
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
		return ColorProposal
	case Prevote:
		return ColorPrevote
	case Precommit:
		return ColorPrecommit
	case Commit:
		return ColorCommit
	case EvidenceMsg:
		return ColorEvidence
	default:
		return ColorReset
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

func SignBytes(msg *Message) []byte {
	return []byte(fmt.Sprintf("%d|%d|%d|%s|%t|%d",
		msg.Type,
		msg.Height,
		msg.Round,
		msg.Block,
		msg.Valid,
		msg.ValidRound,
	))
}
