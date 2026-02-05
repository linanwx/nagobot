package bus

import (
	"context"
	"fmt"
	"sync"

	"github.com/linanwx/nagobot/logger"
)

// Handler is a function that handles events.
type Handler func(ctx context.Context, event *Event)

// Subscription represents a subscription to events.
type Subscription struct {
	ID        string
	EventType EventType
	Handler   Handler
}

// Bus is the central event bus for agent communication.
type Bus struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription
	subCounter    int64

	// Buffered channel for async event processing
	eventChan chan *Event
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewBus creates a new event bus.
func NewBus(bufferSize int) *Bus {
	if bufferSize <= 0 {
		bufferSize = 100
	}

	b := &Bus{
		subscriptions: make(map[string]*Subscription),
		eventChan:     make(chan *Event, bufferSize),
		done:          make(chan struct{}),
	}

	b.wg.Add(1)
	go b.processEvents()

	return b
}

// Subscribe registers a handler for a specific event type.
func (b *Bus) Subscribe(eventType EventType, handler Handler) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subCounter++
	id := fmt.Sprintf("sub-%d", b.subCounter)

	b.subscriptions[id] = &Subscription{
		ID:        id,
		EventType: eventType,
		Handler:   handler,
	}

	logger.Debug("subscription added", "id", id, "eventType", eventType)
	return id
}

// Publish sends an event to the bus asynchronously.
func (b *Bus) Publish(event *Event) {
	select {
	case b.eventChan <- event:
		logger.Debug("event published", "type", event.Type, "source", event.Source)
	case <-b.done:
		logger.Warn("bus closed, event dropped", "type", event.Type)
	default:
		logger.Warn("event buffer full, event dropped", "type", event.Type)
	}
}

// Close shuts down the event bus.
func (b *Bus) Close() {
	close(b.done)
	b.wg.Wait()
}

// processEvents is the main event processing loop.
func (b *Bus) processEvents() {
	defer b.wg.Done()

	for {
		select {
		case event := <-b.eventChan:
			b.dispatch(event)
		case <-b.done:
			// Drain remaining events
			for {
				select {
				case event := <-b.eventChan:
					b.dispatch(event)
				default:
					return
				}
			}
		}
	}
}

// dispatch sends an event to all matching subscribers.
func (b *Bus) dispatch(event *Event) {
	b.mu.RLock()
	subs := make([]*Subscription, 0)
	for _, sub := range b.subscriptions {
		if sub.EventType == event.Type {
			subs = append(subs, sub)
		}
	}
	b.mu.RUnlock()

	ctx := context.Background()
	for _, sub := range subs {
		go func(s *Subscription) {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("handler panic", "subscription", s.ID, "panic", r)
				}
			}()
			s.Handler(ctx, event)
		}(sub)
	}
}
