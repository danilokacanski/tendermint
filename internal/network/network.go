package network

import (
	"crypto/ed25519"
	"fmt"
	mathrand "math/rand"
	"sync"
	"time"

	"Tendermint/internal/types"
)

const (
	defaultSignatureRetryLimit   = 5
	defaultSignatureRetryBackoff = 5 * time.Millisecond
	maxSignatureRetryWindow      = 200 * time.Millisecond
)

type DelayFunc func(from, to int, msg types.Message) time.Duration
type LogFunc func(from, to int, msg types.Message, deliveredAt time.Time)

// Network provides the minimal interface the consensus layer expects from a
// transport implementation.
type Network interface {
	Register(id int, inbox chan<- types.Message, peers []int, pubKey ed25519.PublicKey)
	UpdatePeers(id int, peers []int)
	Broadcast(from int, msg types.Message)
	Unicast(from int, to int, msg types.Message)
	ReportMisbehavior(reporter, offender int, reason string)
	Stop()
}

// NetworkOption mutates a SimulatedNetwork during construction.
type NetworkOption func(*SimulatedNetwork)

// networkEnvelope carries a message and delivery metadata through the
// SimulatedNetwork worker pool.
type networkEnvelope struct {
	from    int
	to      int
	msg     types.Message
	delay   time.Duration
	retries int
}

// SimulatedNetwork multiplexes validator inboxes, injects latency/jitter and
// enforces basic misbehaviour accounting.
type SimulatedNetwork struct {
	mu          sync.RWMutex
	inboxes     map[int]chan<- types.Message
	peers       map[int]map[int]struct{}
	strikeCount map[int]int
	pubKeys     map[int]ed25519.PublicKey

	messages        chan networkEnvelope
	stopOnce        sync.Once
	stopCh          chan struct{}
	wg              sync.WaitGroup
	rngMu           sync.Mutex
	rng             *mathrand.Rand
	broadcastDelay  DelayFunc
	unicastDelay    DelayFunc
	logFunc         LogFunc
	gossipJitter    time.Duration
	maxStrikes      int
	sigRetryLimit   int
	sigRetryBackoff time.Duration
}

// NewSimulatedNetwork constructs a network simulator with the supplied worker
// count and functional options.
func NewSimulatedNetwork(workerCount int, opts ...NetworkOption) *SimulatedNetwork {
	if workerCount <= 0 {
		workerCount = 4
	}
	net := &SimulatedNetwork{
		inboxes:         make(map[int]chan<- types.Message),
		peers:           make(map[int]map[int]struct{}),
		pubKeys:         make(map[int]ed25519.PublicKey),
		strikeCount:     make(map[int]int),
		messages:        make(chan networkEnvelope, 1024),
		stopCh:          make(chan struct{}),
		rng:             mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
		maxStrikes:      3,
		sigRetryLimit:   defaultSignatureRetryLimit,
		sigRetryBackoff: defaultSignatureRetryBackoff,
	}
	for _, opt := range opts {
		opt(net)
	}
	for i := 0; i < workerCount; i++ {
		net.wg.Add(1)
		go net.process()
	}
	return net
}

// WithBroadcastDelay injects a custom delay function for gossip fan-out.
func WithBroadcastDelay(fn DelayFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.broadcastDelay = fn
	}
}

// WithUnicastDelay injects a custom delay function for direct sends.
func WithUnicastDelay(fn DelayFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.unicastDelay = fn
	}
}

// WithNetworkLogger attaches a callback that records every successful delivery.
func WithNetworkLogger(fn LogFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.logFunc = fn
	}
}

// WithRandomSeed fixes the RNG seed to help deterministic test scenarios.
func WithRandomSeed(seed int64) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.rngMu.Lock()
		defer n.rngMu.Unlock()
		n.rng = mathrand.New(mathrand.NewSource(seed))
	}
}

// WithGossipJitter sets an upper bound for random jitter applied to deliveries.
func WithGossipJitter(max time.Duration) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.gossipJitter = max
	}
}

// WithMaxStrikes customises how many misbehaviour reports trigger disconnect.
func WithMaxStrikes(max int) NetworkOption {
	return func(n *SimulatedNetwork) {
		if max > 0 {
			n.maxStrikes = max
		}
	}
}

// Register installs a validator inbox, peer list and optional public key.
func (n *SimulatedNetwork) Register(id int, inbox chan<- types.Message, peers []int, pubKey ed25519.PublicKey) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.inboxes[id] = inbox
	if len(pubKey) > 0 {
		n.pubKeys[id] = append(ed25519.PublicKey(nil), pubKey...)
	}
	n.setPeersLocked(id, peers)
}

// UpdatePeers replaces the adjacency list for a validator (bidirectional).
func (n *SimulatedNetwork) UpdatePeers(id int, peers []int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.setPeersLocked(id, peers)
}

// Broadcast gossips a message using a BFS traversal, applying configured delays.
func (n *SimulatedNetwork) Broadcast(from int, msg types.Message) {
	snapshot := n.peerSnapshot()
	if len(snapshot) == 0 {
		return
	}
	visited := map[int]bool{from: true}
	queue := []int{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, neighbor := range snapshot[current] {
			if visited[neighbor] {
				continue
			}
			delay := time.Duration(0)
			if n.broadcastDelay != nil {
				delay += n.broadcastDelay(current, neighbor, msg)
			}
			delay += n.randomJitter()
			n.enqueue(networkEnvelope{from: from, to: neighbor, msg: msg, delay: delay})
			visited[neighbor] = true
			queue = append(queue, neighbor)
		}
	}
}

// Unicast sends a point-to-point message if a direct edge exists.
func (n *SimulatedNetwork) Unicast(from int, to int, msg types.Message) {
	if !n.hasDirectPeer(from, to) {
		n.recordMisbehavior(from, fmt.Sprintf("attempted to unicast to non-peer %d", to))
		return
	}
	delay := time.Duration(0)
	if n.unicastDelay != nil {
		delay = n.unicastDelay(from, to, msg)
	}
	delay += n.randomJitter()
	n.enqueue(networkEnvelope{from: from, to: to, msg: msg, delay: delay})
}

// ReportMisbehavior increments the strike counter for the offender.
func (n *SimulatedNetwork) ReportMisbehavior(reporter, offender int, reason string) {
	n.recordMisbehavior(offender, fmt.Sprintf("reported by %d: %s", reporter, reason))
}

// Stop shuts down delivery workers and waits for them to exit.
func (n *SimulatedNetwork) Stop() {
	n.stopOnce.Do(func() {
		close(n.stopCh)
		n.wg.Wait()
	})
}

// setPeersLocked updates adjacency entries while holding the write lock.
func (n *SimulatedNetwork) setPeersLocked(id int, peers []int) {
	if _, ok := n.peers[id]; !ok {
		n.peers[id] = make(map[int]struct{})
	}
	current := n.peers[id]
	for peer := range current {
		delete(n.peers[peer], id)
	}
	for peer := range current {
		delete(current, peer)
	}
	for _, peer := range peers {
		if peer == id {
			continue
		}
		if _, ok := n.peers[peer]; !ok {
			n.peers[peer] = make(map[int]struct{})
		}
		current[peer] = struct{}{}
		n.peers[peer][id] = struct{}{}
	}
}

// peerSnapshot returns a copy of the adjacency list suitable for iteration.
func (n *SimulatedNetwork) peerSnapshot() map[int][]int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	snapshot := make(map[int][]int, len(n.peers))
	for id, neighbors := range n.peers {
		list := make([]int, 0, len(neighbors))
		for peer := range neighbors {
			if n.inboxes[peer] == nil {
				continue
			}
			list = append(list, peer)
		}
		snapshot[id] = list
	}
	return snapshot
}

// hasDirectPeer checks whether two validators share a direct link.
func (n *SimulatedNetwork) hasDirectPeer(id, peer int) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	neighbors, ok := n.peers[id]
	if !ok {
		return false
	}
	_, exists := neighbors[peer]
	return exists
}

// recordMisbehavior increments strike counters and disconnects when necessary.
func (n *SimulatedNetwork) recordMisbehavior(offender int, reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.strikeCount[offender]++
	if n.maxStrikes > 0 && n.strikeCount[offender] >= n.maxStrikes {
		n.disconnectLocked(offender)
		fmt.Printf("[NET] disconnecting node %d due to misbehavior (%s)\n", offender, reason)
	} else {
		fmt.Printf("[NET] misbehavior strike for node %d (%s) [%d/%d]\n", offender, reason, n.strikeCount[offender], n.maxStrikes)
	}
}

// disconnectLocked removes a validator from the network while holding the lock.
func (n *SimulatedNetwork) disconnectLocked(id int) {
	delete(n.inboxes, id)
	neighbors, ok := n.peers[id]
	if ok {
		for peer := range neighbors {
			delete(n.peers[peer], id)
		}
	}
	delete(n.peers, id)
	delete(n.pubKeys, id)
}

// randomJitter samples a jitter duration from [0, gossipJitter].
func (n *SimulatedNetwork) randomJitter() time.Duration {
	if n.gossipJitter <= 0 || n.rng == nil {
		return 0
	}
	max := int64(n.gossipJitter)
	if max <= 0 {
		return 0
	}
	n.rngMu.Lock()
	delta := n.rng.Int63n(max + 1)
	n.rngMu.Unlock()
	return time.Duration(delta)
}

// signatureRetryDelay returns exponential backoff for missing pubkey retries.
func (n *SimulatedNetwork) signatureRetryDelay(retry int) time.Duration {
	base := n.sigRetryBackoff
	if base <= 0 {
		base = defaultSignatureRetryBackoff
	}
	if retry <= 0 {
		return base
	}
	delay := time.Duration(1<<(retry-1)) * base
	if delay > maxSignatureRetryWindow {
		delay = maxSignatureRetryWindow
	}
	return delay
}

// enqueue schedules delivery of a network envelope unless the network is
// stopping.
func (n *SimulatedNetwork) enqueue(env networkEnvelope) {
	select {
	case <-n.stopCh:
		return
	default:
	}

	select {
	case n.messages <- env:
	case <-n.stopCh:
	}
}

// process is the worker loop that pulls envelopes and delivers them.
func (n *SimulatedNetwork) process() {
	defer n.wg.Done()
	for {
		select {
		case env := <-n.messages:
			n.deliver(env)
		case <-n.stopCh:
			return
		}
	}
}

// deliver performs delayed delivery with signature verification and strike
// accounting.
func (n *SimulatedNetwork) deliver(env networkEnvelope) {
	if env.delay > 0 {
		timer := time.NewTimer(env.delay)
		select {
		case <-timer.C:
		case <-n.stopCh:
			timer.Stop()
			return
		}
	}

	n.mu.RLock()
	inbox := n.inboxes[env.to]
	pub := n.pubKeys[env.from]
	n.mu.RUnlock()

	if inbox == nil {
		return
	}
	if len(pub) == 0 {
		if env.retries < n.sigRetryLimit {
			env.retries++
			env.delay = n.signatureRetryDelay(env.retries)
			n.enqueue(env)
			return
		}
		n.recordMisbehavior(env.from, "missing public key for signature verification")
		return
	}
	if len(env.msg.Signature) == 0 {
		n.recordMisbehavior(env.from, "missing signature")
		return
	}
	if !ed25519.Verify(pub, types.SignBytes(&env.msg), env.msg.Signature) {
		n.recordMisbehavior(env.from, "invalid signature")
		return
	}

	if n.logFunc != nil && env.msg.Type.Label() != "Unknown" {
		n.logFunc(env.from, env.to, env.msg, time.Now())
	}

	select {
	case inbox <- env.msg:
	case <-n.stopCh:
	}
}

func DefaultNetworkLogger(from, to int, msg types.Message, deliveredAt time.Time) {
	// DefaultNetworkLogger prints a concise log line for message deliveries.
	fmt.Printf("[NET][H%d R%d][%s] %d → %d @ %s\n",
		msg.Height,
		msg.Round,
		msg.Type.Label(),
		from,
		to,
		deliveredAt.Format("15:04:05.000"),
	)
}
