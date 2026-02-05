package cron

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// HealthStatus represents the health status of a component.
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusDegraded  HealthStatus = "degraded"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// HealthCheck is a function that checks the health of a component.
type HealthCheck func(ctx context.Context) error

// ComponentHealth represents the health of a single component.
type ComponentHealth struct {
	Name      string       `json:"name"`
	Status    HealthStatus `json:"status"`
	Message   string       `json:"message,omitempty"`
	LastCheck time.Time    `json:"lastCheck"`
	Latency   int64        `json:"latencyMs"`
}

// SystemHealth represents overall system health.
type SystemHealth struct {
	Status     HealthStatus                `json:"status"`
	Uptime     time.Duration               `json:"uptime"`
	Components map[string]*ComponentHealth `json:"components"`
	Memory     MemoryStats                 `json:"memory"`
	Goroutines int                         `json:"goroutines"`
	Timestamp  time.Time                   `json:"timestamp"`
}

// MemoryStats contains memory usage information.
type MemoryStats struct {
	Alloc      uint64 `json:"allocBytes"`
	TotalAlloc uint64 `json:"totalAllocBytes"`
	Sys        uint64 `json:"sysBytes"`
	NumGC      uint32 `json:"numGC"`
}

// Heartbeat monitors system health and sends periodic heartbeats.
type Heartbeat struct {
	mu         sync.RWMutex
	checks     map[string]HealthCheck
	components map[string]*ComponentHealth
	interval   time.Duration
	startTime  time.Time
	running    bool
	done       chan struct{}
	wg         sync.WaitGroup
	onUnhealthy func(health *SystemHealth)
}

// HeartbeatConfig contains heartbeat configuration.
type HeartbeatConfig struct {
	Interval    time.Duration                // Check interval (default: 30s)
	OnUnhealthy func(health *SystemHealth)   // Callback when unhealthy
}

// NewHeartbeat creates a new heartbeat monitor.
func NewHeartbeat(cfg HeartbeatConfig) *Heartbeat {
	interval := cfg.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}

	return &Heartbeat{
		checks:      make(map[string]HealthCheck),
		components:  make(map[string]*ComponentHealth),
		interval:    interval,
		done:        make(chan struct{}),
		onUnhealthy: cfg.OnUnhealthy,
	}
}

// Register registers a health check for a component.
func (h *Heartbeat) Register(name string, check HealthCheck) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.checks[name] = check
	h.components[name] = &ComponentHealth{
		Name:   name,
		Status: HealthStatusHealthy,
	}

	logger.Debug("health check registered", "component", name)
}

// Start begins the heartbeat monitoring loop.
func (h *Heartbeat) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return fmt.Errorf("heartbeat already running")
	}
	h.running = true
	h.startTime = time.Now()
	h.done = make(chan struct{})
	h.mu.Unlock()

	logger.Info("heartbeat started", "interval", h.interval)

	// Run initial check
	h.runChecks(ctx)

	h.wg.Add(1)
	go h.run(ctx)

	return nil
}

// Stop stops the heartbeat monitor.
func (h *Heartbeat) Stop() error {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return nil
	}
	h.running = false
	close(h.done)
	h.mu.Unlock()

	h.wg.Wait()
	logger.Info("heartbeat stopped")
	return nil
}

// Health returns the current system health.
func (h *Heartbeat) Health() *SystemHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Copy component health
	components := make(map[string]*ComponentHealth)
	overallStatus := HealthStatusHealthy
	for name, comp := range h.components {
		components[name] = &ComponentHealth{
			Name:      comp.Name,
			Status:    comp.Status,
			Message:   comp.Message,
			LastCheck: comp.LastCheck,
			Latency:   comp.Latency,
		}

		// Determine overall status
		if comp.Status == HealthStatusUnhealthy {
			overallStatus = HealthStatusUnhealthy
		} else if comp.Status == HealthStatusDegraded && overallStatus != HealthStatusUnhealthy {
			overallStatus = HealthStatusDegraded
		}
	}

	// Get memory stats
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return &SystemHealth{
		Status:     overallStatus,
		Uptime:     time.Since(h.startTime),
		Components: components,
		Memory: MemoryStats{
			Alloc:      mem.Alloc,
			TotalAlloc: mem.TotalAlloc,
			Sys:        mem.Sys,
			NumGC:      mem.NumGC,
		},
		Goroutines: runtime.NumGoroutine(),
		Timestamp:  time.Now(),
	}
}

// run is the main heartbeat loop.
func (h *Heartbeat) run(ctx context.Context) {
	defer h.wg.Done()

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.done:
			return
		case <-ticker.C:
			h.runChecks(ctx)
		}
	}
}

// runChecks executes all registered health checks.
func (h *Heartbeat) runChecks(ctx context.Context) {
	h.mu.RLock()
	checks := make(map[string]HealthCheck)
	for name, check := range h.checks {
		checks[name] = check
	}
	h.mu.RUnlock()

	var wg sync.WaitGroup
	for name, check := range checks {
		wg.Add(1)
		go func(name string, check HealthCheck) {
			defer wg.Done()
			h.runSingleCheck(ctx, name, check)
		}(name, check)
	}
	wg.Wait()

	// Check if system is unhealthy
	health := h.Health()
	if health.Status == HealthStatusUnhealthy && h.onUnhealthy != nil {
		h.onUnhealthy(health)
	}
}

// runSingleCheck executes a single health check.
func (h *Heartbeat) runSingleCheck(ctx context.Context, name string, check HealthCheck) {
	start := time.Now()

	// Create timeout context
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := check(checkCtx)
	latency := time.Since(start).Milliseconds()

	h.mu.Lock()
	comp := h.components[name]
	if comp == nil {
		comp = &ComponentHealth{Name: name}
		h.components[name] = comp
	}

	comp.LastCheck = time.Now()
	comp.Latency = latency

	if err != nil {
		if checkCtx.Err() == context.DeadlineExceeded {
			comp.Status = HealthStatusUnhealthy
			comp.Message = "health check timed out"
		} else {
			comp.Status = HealthStatusUnhealthy
			comp.Message = err.Error()
		}
		logger.Warn("health check failed", "component", name, "err", err)
	} else {
		comp.Status = HealthStatusHealthy
		comp.Message = ""
	}
	h.mu.Unlock()
}

// ============================================================================
// Common Health Checks
// ============================================================================

// PingCheck creates a simple ping health check.
func PingCheck() HealthCheck {
	return func(ctx context.Context) error {
		return nil
	}
}

// MemoryCheck creates a health check that fails if memory exceeds the limit.
func MemoryCheck(maxBytes uint64) HealthCheck {
	return func(ctx context.Context) error {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		if mem.Alloc > maxBytes {
			return fmt.Errorf("memory usage %d exceeds limit %d", mem.Alloc, maxBytes)
		}
		return nil
	}
}

// GoroutineCheck creates a health check that fails if goroutines exceed the limit.
func GoroutineCheck(maxGoroutines int) HealthCheck {
	return func(ctx context.Context) error {
		count := runtime.NumGoroutine()
		if count > maxGoroutines {
			return fmt.Errorf("goroutine count %d exceeds limit %d", count, maxGoroutines)
		}
		return nil
	}
}
