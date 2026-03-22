package thread

// turnHook runs during message construction and returns messages to inject in
// the current turn.
type turnHook func(ctx turnContext) []string

// turnContext carries read-only request context for hook evaluation.
type turnContext struct {
	ThreadID string

	SessionKey  string
	SessionPath string
	UserMessage string

	SessionEstimatedTokens int
	RequestEstimatedTokens int
	ContextWindowTokens    int
	WarnToken              int
}

// registerHook adds a hook for this thread.
func (t *Thread) registerHook(h turnHook) {
	if h == nil {
		return
	}
	t.mu.Lock()
	t.hooks = append(t.hooks, h)
	t.mu.Unlock()
}

func (t *Thread) runHooks(ctx turnContext) []string {
	t.mu.Lock()
	hooks := make([]turnHook, len(t.hooks))
	copy(hooks, t.hooks)
	t.mu.Unlock()

	var injected []string
	for _, h := range hooks {
		if messages := h(ctx); len(messages) > 0 {
			injected = append(injected, messages...)
		}
	}
	return injected
}
