package network

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"Tendermint/internal/types"
)

func TestSimulatedNetworkRetriesUntilPubKeyAvailable(t *testing.T) {
	net := NewSimulatedNetwork(1, WithGossipJitter(0))
	defer net.Stop()

	senderInbox := make(chan types.Message, 1)
	receiverInbox := make(chan types.Message, 1)

	receiverPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to create receiver key: %v", err)
	}
	senderPub, senderPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to create sender key: %v", err)
	}

	net.Register(2, receiverInbox, []int{1}, receiverPub)
	net.Register(1, senderInbox, []int{2}, nil)

	msg := types.Message{
		From:   1,
		Type:   types.Proposal,
		Height: 1,
		Round:  1,
		Block:  "B1",
		Valid:  true,
	}
	msg.Signature = ed25519.Sign(senderPriv, types.SignBytes(&msg))

	go func() {
		time.Sleep(10 * time.Millisecond)
		net.Register(1, senderInbox, []int{2}, senderPub)
	}()

	net.Broadcast(1, msg)

	select {
	case received := <-receiverInbox:
		if received.Block != msg.Block || received.Type != msg.Type {
			t.Fatalf("unexpected message delivered: %+v", received)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatalf("expected message to be delivered after pubkey registration")
	}

	if strikes := net.strikeCount[1]; strikes != 0 {
		t.Fatalf("expected no misbehavior strikes, got %d", strikes)
	}
}

func TestSimulatedNetworkInvalidSignatureCountsStrike(t *testing.T) {
	net := NewSimulatedNetwork(1, WithGossipJitter(0))
	defer net.Stop()

	senderInbox := make(chan types.Message, 1)
	receiverInbox := make(chan types.Message, 1)

	senderPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to create sender key: %v", err)
	}
	receiverPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to create receiver key: %v", err)
	}

	net.Register(1, senderInbox, []int{2}, senderPub)
	net.Register(2, receiverInbox, []int{1}, receiverPub)

	msg := types.Message{
		From:       1,
		Type:       types.Proposal,
		Height:     2,
		Round:      3,
		Block:      "B2",
		Valid:      true,
		ValidRound: 1,
		Signature:  []byte("invalid"),
	}

	net.Broadcast(1, msg)
	time.Sleep(20 * time.Millisecond)

	if strikes := net.strikeCount[1]; strikes != 1 {
		t.Fatalf("expected one misbehavior strike, got %d", strikes)
	}

	select {
	case <-receiverInbox:
		t.Fatalf("message with invalid signature should not be delivered")
	default:
	}
}
