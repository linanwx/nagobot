package provider

// TokenEstimator is optionally implemented by providers for format-specific
// prompt token estimation. Providers that don't implement this interface
// fall back to the generic tiktoken-based estimation in the thread package.
type TokenEstimator interface {
	EstimatePromptTokens(messages []Message, toolDefs []ToolDef) int
}
