package runtimecfg

import "time"

const (
	AgentDefaultMaxTokens           = 8192
	AgentDefaultTemperature         = 0.95
	AgentDefaultContextWindowTokens = 128000
	AgentDefaultContextWarnRatio    = 0.8
)

const (
	CLIChannelMessageBufferSize      = 10
	CLIChannelStopWaitTimeout        = 500 * time.Millisecond
	TelegramChannelMessageBufferSize = 100
	TelegramUpdateTimeoutSeconds     = 30
	TelegramMaxMessageLength         = 4096
)

const (
	ToolExecDefaultTimeoutSeconds = 60
	ToolExecOutputMaxChars        = 50000
)

const (
	ToolWebSearchDefaultMaxResults = 5
	ToolWebSearchHTTPTimeout       = 15 * time.Second
)

const (
	ToolWebFetchHTTPTimeout     = 30 * time.Second
	ToolWebFetchMaxReadBytes    = 500000
	ToolWebFetchMaxContentChars = 100000
)

const (
	ProviderSDKMaxRetries      = 2
	AnthropicFallbackMaxTokens = 1024
)

const (
	WorkspaceSkillsDirName = "skills"
)
