package network

import (
	"fmt"
	mathrand "math/rand"
	"sync"
	"time"

	"Tendermint/internal/types"
)

type DelayFunc func(from, to int, msg types.Message) time.Duration
type LogFunc func(from, to int, msg types.Message, deliveredAt time.Time)

type Network interface {
	Register(id int, inbox chan<- types.Message)
	Broadcast(from int, msg types.Message)
	Unicast(from int, to int, msg types.Message)
	Stop()
}

type NetworkOption func(*SimulatedNetwork)

type networkEnvelope struct {
	from  int
	to    int
	msg   types.Message
	delay time.Duration
}

type SimulatedNetwork struct {
	mu       sync.RWMutex
	inboxes  map[int]chan<- types.Message
	messages chan networkEnvelope
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	rngMu sync.Mutex
	rng   *mathrand.Rand

	broadcastDelay DelayFunc
	unicastDelay   DelayFunc
	logFunc        LogFunc
	gossipJitter   time.Duration
}

func NewSimulatedNetwork(workerCount int, opts ...NetworkOption) *SimulatedNetwork {
	if workerCount <= 0 {
		workerCount = 4
	}
	net := &SimulatedNetwork{
		inboxes:  make(map[int]chan<- types.Message),
		messages: make(chan networkEnvelope, 1024),
		stopCh:   make(chan struct{}),
		rng:      mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
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

func WithBroadcastDelay(fn DelayFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.broadcastDelay = fn
	}
}

func WithUnicastDelay(fn DelayFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.unicastDelay = fn
	}
}

func WithNetworkLogger(fn LogFunc) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.logFunc = fn
	}
}

func WithRandomSeed(seed int64) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.rngMu.Lock()
		defer n.rngMu.Unlock()
		n.rng = mathrand.New(mathrand.NewSource(seed))
	}
}

func WithGossipJitter(max time.Duration) NetworkOption {
	return func(n *SimulatedNetwork) {
		n.gossipJitter = max
	}
}

func (n *SimulatedNetwork) Register(id int, inbox chan<- types.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.inboxes[id] = inbox
}

func (n *SimulatedNetwork) Broadcast(from int, msg types.Message) {
	receivers := n.activeNodesExcept(from)
	if len(receivers) == 0 {
		return
	}
	n.shuffleReceivers(receivers)
	for _, to := range receivers {
		delay := time.Duration(0)
		if n.broadcastDelay != nil {
			delay = n.broadcastDelay(from, to, msg)
		}
		delay += n.randomJitter()
		n.enqueue(networkEnvelope{from: from, to: to, msg: msg, delay: delay})
	}
}

func (n *SimulatedNetwork) Unicast(from int, to int, msg types.Message) {
	if !n.nodeExists(to) {
		return
	}
	delay := time.Duration(0)
	if n.unicastDelay != nil {
		delay = n.unicastDelay(from, to, msg)
	}
	delay += n.randomJitter()
	n.enqueue(networkEnvelope{from: from, to: to, msg: msg, delay: delay})
}

func (n *SimulatedNetwork) Stop() {
	n.stopOnce.Do(func() {
		close(n.stopCh)
		n.wg.Wait()
	})
}

func (n *SimulatedNetwork) activeNodesExcept(skip int) []int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.inboxes) == 0 {
		return nil
	}
	receivers := make([]int, 0, len(n.inboxes)-1)
	for id := range n.inboxes {
		if id == skip {
			continue
		}
		receivers = append(receivers, id)
	}
	return receivers
}

func (n *SimulatedNetwork) nodeExists(id int) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	_, ok := n.inboxes[id]
	return ok
}

func (n *SimulatedNetwork) shuffleReceivers(ids []int) {
	if len(ids) <= 1 || n.rng == nil {
		return
	}
	n.rngMu.Lock()
	n.rng.Shuffle(len(ids), func(i, j int) {
		ids[i], ids[j] = ids[j], ids[i]
	})
	n.rngMu.Unlock()
}

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
	n.mu.RUnlock()

	if inbox == nil {
		return
	}

	if n.logFunc != nil {
		n.logFunc(env.from, env.to, env.msg, time.Now())
	}

	select {
	case inbox <- env.msg:
	case <-n.stopCh:
	}
}

func DefaultNetworkLogger(from, to int, msg types.Message, deliveredAt time.Time) {
	fmt.Printf("[NET][H%d R%d][%s] %d → %d @ %s\n",
		msg.Height,
		msg.Round,
		msg.Type.Label(),
		from,
		to,
		deliveredAt.Format("15:04:05.000"),
	)
}
