package thread

// TurnHook runs during message construction and returns messages to inject in
// the current turn.
type TurnHook func(ctx TurnContext) []string

// TurnContext carries read-only request context for hook evaluation.
type TurnContext struct {
	ThreadID string

	SessionKey  string
	SessionPath string
	UserMessage string

	SessionEstimatedTokens int
	RequestEstimatedTokens int
	ContextWindowTokens    int
	ContextWarnRatio       float64
}

// RegisterHook adds a hook for this thread.
func (t *Thread) RegisterHook(h TurnHook) {
	if h == nil {
		return
	}
	t.mu.Lock()
	t.hooks = append(t.hooks, h)
	t.mu.Unlock()
}

func (t *Thread) runHooks(ctx TurnContext) []string {
	t.mu.Lock()
	hooks := make([]TurnHook, len(t.hooks))
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
