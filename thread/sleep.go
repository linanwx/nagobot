package thread

// IsHeartbeatWake returns true if the current turn was triggered by a heartbeat.
func (t *Thread) IsHeartbeatWake() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastWakeSource == WakeHeartbeat
}

// SetSuppressSink marks the current turn to skip sink delivery.
func (t *Thread) SetSuppressSink() {
	t.mu.Lock()
	t.suppressSink = true
	t.mu.Unlock()
}

// isSinkSuppressed returns whether sink delivery is currently suppressed.
func (t *Thread) isSinkSuppressed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suppressSink
}

// checkAndResetSinkSuppressed returns the current suppressSink flag and resets it.
func (t *Thread) checkAndResetSinkSuppressed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.suppressSink
	t.suppressSink = false
	return v
}

// SetHaltLoop signals the Runner to stop after the current tool calls complete.
func (t *Thread) SetHaltLoop() {
	t.mu.Lock()
	t.haltLoop = true
	t.mu.Unlock()
}

// isHaltLoop returns whether the Runner should halt.
func (t *Thread) isHaltLoop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.haltLoop
}

// resetHaltLoop clears the halt flag at the start of each turn.
func (t *Thread) resetHaltLoop() {
	t.mu.Lock()
	t.haltLoop = false
	t.mu.Unlock()
}
