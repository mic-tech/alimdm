package httpapi

import (
	"sync"

	"ali-mdm/server/internal/device"
)

// PokeQueue holds pending per-device commands. The MQTT bridge enqueues a poke
// when an operator pushes a command; the next heartbeat drains it. This keeps
// the device pull-based (robust) while MQTT provides low-latency wake.
type PokeQueue struct {
	mu     sync.Mutex
	queues map[string][]device.Poke
}

func NewPokeQueue() *PokeQueue {
	return &PokeQueue{queues: make(map[string][]device.Poke)}
}

func (q *PokeQueue) Enqueue(deviceID string, p device.Poke) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queues[deviceID] = append(q.queues[deviceID], p)
}

func (q *PokeQueue) Drain(deviceID string) []device.Poke {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.queues[deviceID]
	q.queues[deviceID] = nil
	return out
}
