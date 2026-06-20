// Package sse implements a simple fan-out broker for Server-Sent Events.
// It implements ports.EventPublisher and lets HTTP handlers subscribe to a
// per-launch stream of domain events.
package sse

import (
	"sync"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

const subChannelBuf = 16

// Broker fans domain events out to per-launch subscriber channels.
// Slow subscribers are dropped (non-blocking send) so a stalled client cannot
// block the proposal service.
type Broker struct {
	mu   sync.RWMutex
	subs map[string][]chan domain.DomainEvent // keyed by launchID string
}

// New creates an empty Broker.
func New() *Broker {
	return &Broker{subs: make(map[string][]chan domain.DomainEvent)}
}

// Publish implements ports.EventPublisher. It fans the event out to every
// subscriber registered for the event's launch, dropping slow receivers.
//
// The read lock is held for the entire iteration so that a concurrent
// Unsubscribe (which needs the write lock) cannot close a channel while we
// are sending to it.  Non-blocking sends keep this safe: we never block while
// holding the lock, so Unsubscribe simply waits until the tick finishes.
func (b *Broker) Publish(ev domain.DomainEvent) {
	lid := ev.GetLaunchID().String()
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[lid] {
		select {
		case ch <- ev:
		default: // subscriber is too slow — drop
		}
	}
}

// Subscribe registers a new channel that receives events for launchID.
// The caller is responsible for calling Unsubscribe when done.
func (b *Broker) Subscribe(launchID string) chan domain.DomainEvent {
	ch := make(chan domain.DomainEvent, subChannelBuf)
	b.mu.Lock()
	b.subs[launchID] = append(b.subs[launchID], ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the subscriber list for launchID and closes it.
func (b *Broker) Unsubscribe(launchID string, ch chan domain.DomainEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[launchID]
	for i, c := range list {
		if c == ch {
			b.subs[launchID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(b.subs[launchID]) == 0 {
		delete(b.subs, launchID)
	}
	close(ch)
}
