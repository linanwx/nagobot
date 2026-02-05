package bus

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/pinkplumcom/nagobot/logger"
)

// Handler is a function that handles events.
type Handler func(ctx context.Context, event *Event)

// Subscription represents a subscription to events.
type Subscription struct {
	ID        string
	EventType EventType // Empty means all events
	Handler   Handler
	Filter    func(*Event) bool // Optional filter
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

	// Start the event processor
	b.wg.Add(1)
	go b.processEvents()

	return b
}

// Subscribe registers a handler for events.
func (b *Bus) Subscribe(eventType EventType, handler Handler) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subCounter++
	id := generateSubID(b.subCounter)

	b.subscriptions[id] = &Subscription{
		ID:        id,
		EventType: eventType,
		Handler:   handler,
	}

	logger.Debug("subscription added", "id", id, "eventType", eventType)
	return id
}

// SubscribeWithFilter registers a handler with a custom filter.
func (b *Bus) SubscribeWithFilter(eventType EventType, handler Handler, filter func(*Event) bool) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subCounter++
	id := generateSubID(b.subCounter)

	b.subscriptions[id] = &Subscription{
		ID:        id,
		EventType: eventType,
		Handler:   handler,
		Filter:    filter,
	}

	logger.Debug("subscription added with filter", "id", id, "eventType", eventType)
	return id
}

// SubscribeAll registers a handler for all events.
func (b *Bus) SubscribeAll(handler Handler) string {
	return b.Subscribe("", handler)
}

// Unsubscribe removes a subscription.
func (b *Bus) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscriptions, id)
	logger.Debug("subscription removed", "id", id)
}

// Publish sends an event to the bus.
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

// PublishSync sends an event and waits for all handlers to complete.
func (b *Bus) PublishSync(ctx context.Context, event *Event) {
	b.mu.RLock()
	subs := make([]*Subscription, 0, len(b.subscriptions))
	for _, sub := range b.subscriptions {
		if b.matchSubscription(sub, event) {
			subs = append(subs, sub)
		}
	}
	b.mu.RUnlock()

	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("handler panic", "subscription", s.ID, "panic", r)
				}
			}()
			s.Handler(ctx, event)
		}(sub)
	}
	wg.Wait()
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
		if b.matchSubscription(sub, event) {
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

// matchSubscription checks if a subscription matches an event.
func (b *Bus) matchSubscription(sub *Subscription, event *Event) bool {
	// Check event type (empty means all)
	if sub.EventType != "" && sub.EventType != event.Type {
		return false
	}

	// Check custom filter
	if sub.Filter != nil && !sub.Filter(event) {
		return false
	}

	return true
}

// generateSubID generates a subscription ID.
func generateSubID(counter int64) string {
	return "sub-" + string(rune('a'+counter%26)) + string(rune(counter))
}

// ============================================================================
// Convenience Methods
// ============================================================================

// PublishAgentStarted publishes an agent started event.
func (b *Bus) PublishAgentStarted(agentID string) {
	event, _ := NewEvent(EventAgentStarted, agentID, nil)
	b.Publish(event)
}

// PublishAgentStopped publishes an agent stopped event.
func (b *Bus) PublishAgentStopped(agentID string) {
	event, _ := NewEvent(EventAgentStopped, agentID, nil)
	b.Publish(event)
}

// PublishAgentError publishes an agent error event.
func (b *Bus) PublishAgentError(agentID string, err error, context string) {
	event, _ := NewEvent(EventAgentError, agentID, AgentErrorData{
		Error:   err.Error(),
		Context: context,
	})
	b.Publish(event)
}

// PublishToolCalled publishes a tool called event.
func (b *Bus) PublishToolCalled(agentID, toolName string, args any) {
	event, _ := NewEvent(EventToolCalled, agentID, ToolEventData{
		ToolName:  toolName,
		Arguments: mustMarshal(args),
	})
	b.Publish(event)
}

// PublishToolCompleted publishes a tool completed event.
func (b *Bus) PublishToolCompleted(agentID, toolName, result string) {
	event, _ := NewEvent(EventToolCompleted, agentID, ToolEventData{
		ToolName: toolName,
		Result:   result,
	})
	b.Publish(event)
}

// PublishSubagentSpawned publishes a subagent spawned event.
func (b *Bus) PublishSubagentSpawned(parentID, childID, agentType, task string) {
	event, _ := NewEvent(EventSubagentSpawned, parentID, SubagentEventData{
		AgentID:   childID,
		AgentType: agentType,
		Task:      task,
	})
	b.Publish(event)
}

// PublishSubagentCompleted publishes a subagent completed event.
func (b *Bus) PublishSubagentCompleted(parentID, childID, result string) {
	event, _ := NewEvent(EventSubagentCompleted, parentID, SubagentEventData{
		AgentID: childID,
		Result:  result,
	})
	b.Publish(event)
}

// mustMarshal marshals to JSON, returning nil on error.
func mustMarshal(v any) []byte {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}
