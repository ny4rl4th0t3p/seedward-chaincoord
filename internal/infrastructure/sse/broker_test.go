package sse_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/sse"
)

// windowClosed builds a minimal WindowClosed event for the given launchID.
func windowClosed(launchID uuid.UUID) domain.DomainEvent {
	return domain.WindowClosed{LaunchID: launchID}.WithTime(time.Now())
}

// recv waits up to 500ms for one event from ch, failing the test on timeout.
func recv(t *testing.T, ch chan domain.DomainEvent) domain.DomainEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "timed out waiting for event")
		return nil
	}
}

// ---- Subscribe / Publish / Unsubscribe basics --------------------------------

func TestBroker_SubscribeReceivesPublishedEvent(t *testing.T) {
	b := sse.New()
	lid := uuid.New()
	ch := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), ch)

	b.Publish(windowClosed(lid))

	ev := recv(t, ch)
	assert.Equal(t, lid, ev.GetLaunchID())
}

func TestBroker_UnsubscribeClosesChannel(t *testing.T) {
	b := sse.New()
	lid := uuid.New()
	ch := b.Subscribe(lid.String())
	b.Unsubscribe(lid.String(), ch)

	// Channel should be closed — receive should return zero value immediately.
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed")
	case <-time.After(100 * time.Millisecond):
		assert.Fail(t, "channel was not closed after Unsubscribe")
	}
}

func TestBroker_UnsubscribeRemovesSubscriber(t *testing.T) {
	b := sse.New()
	lid := uuid.New()
	ch := b.Subscribe(lid.String())
	b.Unsubscribe(lid.String(), ch)

	// Subscribe a new channel and publish — the old (closed) channel must not interfere.
	ch2 := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), ch2)

	b.Publish(windowClosed(lid))
	recv(t, ch2) // must succeed — old channel is gone
}

func TestBroker_PublishToUnknownLaunchNoPanic(_ *testing.T) {
	b := sse.New()
	// No subscribers at all — should not panic.
	b.Publish(windowClosed(uuid.New()))
}

// ---- Fan-out to multiple subscribers -----------------------------------------

func TestBroker_FanOutToAllSubscribers(t *testing.T) {
	b := sse.New()
	lid := uuid.New()

	ch1 := b.Subscribe(lid.String())
	ch2 := b.Subscribe(lid.String())
	ch3 := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), ch1)
	defer b.Unsubscribe(lid.String(), ch2)
	defer b.Unsubscribe(lid.String(), ch3)

	b.Publish(windowClosed(lid))

	recv(t, ch1)
	recv(t, ch2)
	recv(t, ch3)
}

// ---- Isolation between different launches ------------------------------------

func TestBroker_EventsRoutedByLaunchID(t *testing.T) {
	b := sse.New()
	lid1 := uuid.New()
	lid2 := uuid.New()

	ch1 := b.Subscribe(lid1.String())
	ch2 := b.Subscribe(lid2.String())
	defer b.Unsubscribe(lid1.String(), ch1)
	defer b.Unsubscribe(lid2.String(), ch2)

	b.Publish(windowClosed(lid1))

	// ch1 should receive; ch2 should not.
	recv(t, ch1)
	select {
	case ev := <-ch2:
		assert.Fail(t, "ch2 should not receive events for lid1", "got %v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected — no cross-talk
	}
}

// ---- Slow subscriber (buffer full) -------------------------------------------

func TestBroker_SlowSubscriberEventDropped(t *testing.T) {
	b := sse.New()
	lid := uuid.New()

	// Fill the slow subscriber's buffer completely before publishing.
	slow := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), slow)
	for range cap(slow) {
		slow <- windowClosed(lid)
	}

	fast := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), fast)

	// Publish must return immediately even though `slow` is full.
	done := make(chan struct{})
	go func() {
		b.Publish(windowClosed(lid))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "Publish blocked on slow subscriber")
	}

	// fast subscriber still received the event.
	recv(t, fast)
}

// ---- Multiple publishes ------------------------------------------------------

func TestBroker_MultiplePublishesDeliveredInOrder(t *testing.T) {
	b := sse.New()
	lid := uuid.New()
	ch := b.Subscribe(lid.String())
	defer b.Unsubscribe(lid.String(), ch)

	// Distinguishable events (distinct, increasing timestamps) so delivery ORDER is actually
	// verifiable — 5 identical events would make any delivery order look correct.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const n = 5
	for i := range n {
		b.Publish(domain.WindowClosed{LaunchID: lid}.WithTime(base.Add(time.Duration(i) * time.Second)))
	}
	for i := range n {
		ev := recv(t, ch)
		assert.Equal(t, base.Add(time.Duration(i)*time.Second), ev.OccurredAt(),
			"event %d delivered out of order", i)
	}
}

// ---- Concurrent safety (run with -race) --------------------------------------

func TestBroker_ConcurrentSubscribePublishUnsubscribe(_ *testing.T) {
	b := sse.New()
	lid := uuid.New()

	var wg sync.WaitGroup
	const goroutines = 20

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := b.Subscribe(lid.String())
			b.Publish(windowClosed(lid))
			// Drain any events that arrived before unsubscribe.
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
				default:
					b.Unsubscribe(lid.String(), ch)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestBroker_ConcurrentMultiLaunchPublish(_ *testing.T) {
	b := sse.New()
	const launches = 5
	const pubsPerLaunch = 10

	var wg sync.WaitGroup
	for range launches {
		lid := uuid.New()
		ch := b.Subscribe(lid.String())

		wg.Add(1)
		go func(id uuid.UUID, c chan domain.DomainEvent) {
			defer wg.Done()
			defer b.Unsubscribe(id.String(), c)
			for range pubsPerLaunch {
				b.Publish(windowClosed(id))
			}
		}(lid, ch)
	}
	wg.Wait()
}
