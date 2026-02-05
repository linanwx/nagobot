package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// SubagentStatus represents the status of a subagent.
type SubagentStatus string

const (
	SubagentStatusPending   SubagentStatus = "pending"
	SubagentStatusRunning   SubagentStatus = "running"
	SubagentStatusCompleted SubagentStatus = "completed"
	SubagentStatusFailed    SubagentStatus = "failed"
)

// SubagentOrigin tracks where the spawning request came from,
// so async results can be pushed back to the correct channel/chat.
type SubagentOrigin struct {
	Channel    string // Channel name (e.g., "telegram")
	ReplyTo    string // Chat/user ID for reply routing
	SessionKey string // Session key for context
}

// SubagentTask represents a task for a subagent.
type SubagentTask struct {
	ID          string
	ParentID    string
	Type        string // Agent name (corresponds to agents/*.md file)
	Task        string // The task description/prompt
	Context     string // Additional context
	Origin      SubagentOrigin
	Timeout     time.Duration
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	Status      SubagentStatus
	Result      string
	Error       string
}

// SubagentRunner is a function that runs a subagent task.
type SubagentRunner func(ctx context.Context, task *SubagentTask) (string, error)

// SubagentManager manages subagent lifecycle.
type SubagentManager struct {
	mu            sync.RWMutex
	bus           *Bus
	tasks         map[string]*SubagentTask
	runner        SubagentRunner // Single runner for all subagents
	counter       int64
	maxConcurrent int
	semaphore     chan struct{}
}

// NewSubagentManager creates a new subagent manager.
func NewSubagentManager(bus *Bus, maxConcurrent int, runner SubagentRunner) *SubagentManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 5
	}

	return &SubagentManager{
		bus:           bus,
		tasks:         make(map[string]*SubagentTask),
		runner:        runner,
		maxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// SpawnWithOrigin creates and starts a new subagent task with origin routing info.
func (m *SubagentManager) SpawnWithOrigin(ctx context.Context, parentID, task, taskContext, agentName string, origin SubagentOrigin) (string, error) {
	return m.spawnInternal(ctx, parentID, task, taskContext, agentName, origin)
}

// Spawn creates and starts a new subagent task.
// agentName is the name of the agent definition (from agents/*.md).
func (m *SubagentManager) Spawn(ctx context.Context, parentID, task, taskContext, agentName string) (string, error) {
	return m.spawnInternal(ctx, parentID, task, taskContext, agentName, SubagentOrigin{})
}

func (m *SubagentManager) spawnInternal(ctx context.Context, parentID, task, taskContext, agentName string, origin SubagentOrigin) (string, error) {
	m.mu.Lock()

	// Check if runner is configured
	if m.runner == nil {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent runner not configured")
	}

	// Create task
	m.counter++
	idPart := "task"
	if agentName != "" {
		idPart = agentName
	}
	taskID := fmt.Sprintf("sub-%s-%d", idPart, m.counter)

	subTask := &SubagentTask{
		ID:        taskID,
		ParentID:  parentID,
		Type:      agentName, // Agent name from agents/ directory
		Task:      task,
		Context:   taskContext,
		Origin:    origin,
		Timeout:   5 * time.Minute, // Default timeout
		CreatedAt: time.Now(),
		Status:    SubagentStatusPending,
	}

	m.tasks[taskID] = subTask
	m.mu.Unlock()

	// Run in background
	go m.runTask(ctx, subTask, m.runner)

	logger.Info("subagent spawned", "id", taskID, "agent", agentName, "parent", parentID)
	return taskID, nil
}

// SpawnSync creates and waits for a subagent task to complete.
func (m *SubagentManager) SpawnSync(ctx context.Context, parentID, task, taskContext, agentName string) (string, error) {
	taskID, err := m.Spawn(ctx, parentID, task, taskContext, agentName)
	if err != nil {
		return "", err
	}

	return m.Wait(ctx, taskID)
}

// Wait waits for a task to complete and returns the result.
func (m *SubagentManager) Wait(ctx context.Context, taskID string) (string, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			m.mu.RLock()
			task, ok := m.tasks[taskID]
			m.mu.RUnlock()

			if !ok {
				return "", fmt.Errorf("task not found: %s", taskID)
			}

			switch task.Status {
			case SubagentStatusCompleted:
				return task.Result, nil
			case SubagentStatusFailed:
				return "", fmt.Errorf("subagent failed: %s", task.Error)
			}
		}
	}
}

// GetTask returns a task by ID.
func (m *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[taskID]
	return task, ok
}

// runTask executes a subagent task.
func (m *SubagentManager) runTask(ctx context.Context, task *SubagentTask, runner SubagentRunner) {
	// Acquire semaphore
	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		m.setTaskFailed(task, ctx.Err().Error())
		return
	}

	// Update status to running
	m.mu.Lock()
	task.Status = SubagentStatusRunning
	task.StartedAt = time.Now()
	m.mu.Unlock()

	// Create timeout context
	runCtx := ctx
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, task.Timeout)
		defer cancel()
	}

	// Run the task
	result, err := runner(runCtx, task)

	// Update task status
	m.mu.Lock()
	task.CompletedAt = time.Now()
	if err != nil {
		task.Status = SubagentStatusFailed
		task.Error = err.Error()
		logger.Error("subagent failed", "id", task.ID, "err", err)
	} else {
		task.Status = SubagentStatusCompleted
		task.Result = result
		logger.Info("subagent completed", "id", task.ID)
	}
	m.mu.Unlock()

	// Publish completion event (including full origin for push delivery + session routing)
	if m.bus != nil {
		if err != nil {
			event, _ := NewEvent(EventSubagentError, task.ParentID, SubagentEventData{
				AgentID:          task.ID,
				Error:            err.Error(),
				OriginChannel:    task.Origin.Channel,
				OriginReplyTo:    task.Origin.ReplyTo,
				OriginSessionKey: task.Origin.SessionKey,
			})
			m.bus.Publish(event)
		} else {
			event, _ := NewEvent(EventSubagentCompleted, task.ParentID, SubagentEventData{
				AgentID:          task.ID,
				Result:           result,
				OriginChannel:    task.Origin.Channel,
				OriginReplyTo:    task.Origin.ReplyTo,
				OriginSessionKey: task.Origin.SessionKey,
			})
			m.bus.Publish(event)
		}
	}
}

// setTaskFailed marks a task as failed.
func (m *SubagentManager) setTaskFailed(task *SubagentTask, errMsg string) {
	m.mu.Lock()
	task.Status = SubagentStatusFailed
	task.Error = errMsg
	task.CompletedAt = time.Now()
	m.mu.Unlock()

	if m.bus != nil {
		event, _ := NewEvent(EventSubagentError, task.ParentID, SubagentEventData{
			AgentID:          task.ID,
			Error:            errMsg,
			OriginChannel:    task.Origin.Channel,
			OriginReplyTo:    task.Origin.ReplyTo,
			OriginSessionKey: task.Origin.SessionKey,
		})
		m.bus.Publish(event)
	}
}

