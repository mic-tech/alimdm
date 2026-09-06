package httpapi

import (
	"sync"

	"ali-mdm/server/internal/device"
)

// PokeQueue holds pending per-device work notices, and wakes the device that
// they are for.
//
// Two halves of one idea. The queue is drained by the next heartbeat, which is
// what makes the system robust: a tablet that has been asleep, offline or
// behind a captive portal picks up everything waiting for it the moment it can
// talk, with no state on the wire to get out of step.
//
// The wake half is what makes it quick. A device holding the events stream open
// is told immediately that something is waiting, and heartbeats there and then
// instead of up to thirty seconds later. It carries no payload on purpose: the
// stream says "there is work", the heartbeat says what — so a missed or dropped
// notification costs latency and nothing else, and the poll is still the thing
// that actually delivers.
//
// Notifying from here rather than at the call sites means every path that
// queues work — commands, file pushes, installs, streams, whatever is added
// next — wakes the device without having to remember to.
type PokeQueue struct {
	mu      sync.Mutex
	queues  map[string][]device.Poke
	waiters map[string]map[chan struct{}]struct{}
}

func NewPokeQueue() *PokeQueue {
	return &PokeQueue{
		queues:  make(map[string][]device.Poke),
		waiters: make(map[string]map[chan struct{}]struct{}),
	}
}

func (q *PokeQueue) Enqueue(deviceID string, p device.Poke) {
	q.mu.Lock()
	q.queues[deviceID] = append(q.queues[deviceID], p)
	waiters := make([]chan struct{}, 0, len(q.waiters[deviceID]))
	for ch := range q.waiters[deviceID] {
		waiters = append(waiters, ch)
	}
	q.mu.Unlock()

	// Outside the lock, and never blocking: a waiter with a notice already
	// pending needs no second one, and a slow reader must not be able to hold
	// up the operator's request.
	for _, ch := range waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (q *PokeQueue) Drain(deviceID string) []device.Poke {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.queues[deviceID]
	q.queues[deviceID] = nil
	return out
}

// Peek reports what is waiting without taking it. Draining belongs to the
// heartbeat: the stream only ever says "there is work", so it must not consume
// the very notice the heartbeat is coming to collect.
func (q *PokeQueue) Peek(deviceID string) []device.Poke {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queues[deviceID]
}

// Subscribe returns a channel that receives a value whenever work is queued for
// a device, and a function to stop listening. The channel is buffered by one,
// so a notice is never lost between reads and never queues up either.
func (q *PokeQueue) Subscribe(deviceID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	q.mu.Lock()
	if q.waiters[deviceID] == nil {
		q.waiters[deviceID] = make(map[chan struct{}]struct{})
	}
	q.waiters[deviceID][ch] = struct{}{}
	q.mu.Unlock()

	return ch, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if set := q.waiters[deviceID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(q.waiters, deviceID)
			}
		}
	}
}

// Waiting reports how many streams are listening for a device. Used by the
// console to say whether a command will land now or at the next check-in.
func (q *PokeQueue) Waiting(deviceID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiters[deviceID])
}
