package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread"
	"github.com/spf13/cobra"
)

var sessionStatsCmd = &cobra.Command{
	Use:     "session-stats <key>",
	Short:   "Show context usage stats for a session",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runSessionStats,
}

func init() {
	rootCmd.AddCommand(sessionStatsCmd)
}

type sessionStatsOutput struct {
	SessionKey          string            `json:"session_key"`
	ModelResolution     *modelResolution  `json:"model_resolution"`
	MessageCount        int               `json:"message_count"`
	RoleCounts          map[string]int    `json:"role_counts"`
	CompressedMessages  int               `json:"compressed_messages"`
	HeartbeatTrimmed    int               `json:"heartbeat_trimmed"`
	RoleTokens          map[string]int    `json:"role_tokens"`
	SystemPromptTokens  int               `json:"system_prompt_tokens"`
	RawTokens           int               `json:"raw_tokens"`
	CompressedTokens    int               `json:"compressed_tokens"`
	TokensSaved         int               `json:"tokens_saved"`
	ContextWindowTokens int               `json:"context_window_tokens"`
	UsageRatio          float64           `json:"usage_ratio"`
	WarnToken           int               `json:"warn_token"`
	PressureStatus      string            `json:"pressure_status"`
	LongestMessages     []longestMsgEntry `json:"longest_messages"`
}

type modelResolution struct {
	Steps            []resolutionStep `json:"steps"`
	ResolvedProvider string           `json:"resolved_provider"`
	ResolvedModel    string           `json:"resolved_model"`
	ResolvedCtxWindow int             `json:"resolved_context_window"`
	IsDefault        bool             `json:"is_default"`
}

type resolutionStep struct {
	Step     string `json:"step"`
	Lookup   string `json:"lookup"`
	Found    string `json:"found"`
	Status   string `json:"status"`
	Fallback string `json:"fallback,omitempty"`
}

type longestMsgEntry struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Tokens    int    `json:"tokens"`
	Chars     int    `json:"chars"`
	Snippet   string `json:"snippet"`
}

func runSessionStats(_ *cobra.Command, args []string) error {
	key := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	messages, _, err := loadSessionMessages(workspace, key)
	if err != nil {
		return err
	}

	roleCounts := map[string]int{}
	compressedCount := 0
	heartbeatTrimCount := 0
	for _, m := range messages {
		roleCounts[m.Role]++
		if m.Compressed != "" {
			compressedCount++
		}
		if m.HeartbeatTrim {
			heartbeatTrimCount++
		}
	}

	rawTokens := thread.EstimateMessagesTokens(messages)
	compressed := thread.ApplyCompressed(provider.SanitizeMessages(messages))
	compressedTokens := thread.EstimateMessagesTokens(compressed)

	roleTokens := map[string]int{}
	for _, m := range compressed {
		roleTokens[m.Role] += thread.EstimateMessageTokens(m)
	}

	registry := agent.NewRegistry(workspace)
	resolution := resolveModelChain(cfg, registry, key)
	systemPromptTokens := estimateSystemPrompt(registry, resolution.agentName)

	// Use the resolved model for context window, not the global default.
	contextWindow := resolution.ResolvedCtxWindow
	ct := thread.ComputeContextThresholds(contextWindow)
	usageRatio := float64(compressedTokens) / float64(contextWindow)

	status := thread.PressureStatus(compressedTokens, ct)

	// Find top 3 longest messages (by token count, using compressed view).
	type indexedMsg struct {
		idx    int
		tokens int
	}
	ranked := make([]indexedMsg, len(compressed))
	for i, m := range compressed {
		ranked[i] = indexedMsg{idx: i, tokens: thread.EstimateMessageTokens(m)}
	}
	sort.Slice(ranked, func(a, b int) bool {
		return ranked[a].tokens > ranked[b].tokens
	})
	topN := 3
	if len(ranked) < topN {
		topN = len(ranked)
	}
	longest := make([]longestMsgEntry, topN)
	for i := 0; i < topN; i++ {
		m := compressed[ranked[i].idx]
		orig := messages[ranked[i].idx]
		snippet := []rune(m.Content)
		if len(snippet) > 100 {
			snippet = append(snippet[:100], []rune("...")...)
		}
		longest[i] = longestMsgEntry{
			MessageID: orig.ID,
			Role:      m.Role,
			Tokens:    ranked[i].tokens,
			Chars:     len(m.Content),
			Snippet:   string(snippet),
		}
	}

	output := sessionStatsOutput{
		SessionKey:          key,
		ModelResolution:     resolution.export(),
		MessageCount:        len(messages),
		RoleCounts:          roleCounts,
		CompressedMessages:  compressedCount,
		HeartbeatTrimmed:    heartbeatTrimCount,
		RoleTokens:          roleTokens,
		SystemPromptTokens:  systemPromptTokens,
		RawTokens:           rawTokens,
		CompressedTokens:    compressedTokens,
		TokensSaved:         rawTokens - compressedTokens,
		ContextWindowTokens: contextWindow,
		UsageRatio:          usageRatio,
		WarnToken:           ct.WarnToken,
		PressureStatus:      status,
		LongestMessages:     longest,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// estimateSystemPrompt rebuilds the agent's system prompt and estimates its token count.
// This is approximate because runtime vars (TIME, TOOLS, SKILLS, USER) are not available.
func estimateSystemPrompt(registry *agent.AgentRegistry, agentName string) int {
	a, err := registry.New(agentName)
	if err != nil {
		return 0
	}
	prompt := a.Build()
	return thread.EstimateTextTokens(prompt)
}

// resolveModelChainResult holds both the exported JSON fields and internal state.
type resolveModelChainResult struct {
	modelResolution
	agentName string // for reuse by estimateSystemPrompt
}

func (r *resolveModelChainResult) export() *modelResolution {
	return &r.modelResolution
}

// resolveModelChain replicates the model resolution logic from thread/run.go
// (resolvedModelConfig + resolvedProviderModel) for CLI use.
func resolveModelChain(cfg *config.Config, registry *agent.AgentRegistry, sessionKey string) *resolveModelChainResult {
	result := &resolveModelChainResult{}
	var steps []resolutionStep

	// Step 1: session key → agent name
	agentName := cfg.SessionAgent(sessionKey)
	if agentName != "" {
		steps = append(steps, resolutionStep{
			Step:   "session_agent",
			Lookup: fmt.Sprintf("sessionAgents[%q]", sessionKey),
			Found:  agentName,
			Status: "hit",
		})
	} else {
		agentName = "soul"
		steps = append(steps, resolutionStep{
			Step:     "session_agent",
			Lookup:   fmt.Sprintf("sessionAgents[%q]", sessionKey),
			Found:    "",
			Status:   "miss",
			Fallback: "soul",
		})
	}
	result.agentName = agentName

	// Step 2: agent name → specialty
	var specialty string
	def := registry.Def(agentName)
	if def != nil && def.Specialty != "" {
		specialty = def.Specialty
		// Show which file the agent was loaded from.
		label := def.Path
		if label != "" {
			// Shorten to relative-ish form: agents-builtin/coffee.md or agents/coffee.md
			if i := findAgentPathPrefix(label); i >= 0 {
				label = label[i:]
			}
		}
		steps = append(steps, resolutionStep{
			Step:   "agent_specialty",
			Lookup: fmt.Sprintf("%s → specialty", label),
			Found:  specialty,
			Status: "hit",
		})
	} else {
		reason := ""
		if def == nil {
			reason = "agent not found in registry"
		} else {
			reason = "no specialty in frontmatter"
		}
		steps = append(steps, resolutionStep{
			Step:     "agent_specialty",
			Lookup:   fmt.Sprintf("agents/%s.md → specialty", agentName),
			Found:    "",
			Status:   "miss",
			Fallback: reason,
		})
	}

	// Step 3: specialty → model routing
	resolvedProvider := cfg.GetProvider()
	resolvedModel := cfg.GetModelName()
	isDefault := true

	if specialty != "" {
		models := cfg.Thread.Models
		if mc, ok := models[specialty]; ok && mc != nil {
			resolvedProvider = mc.Provider
			resolvedModel = mc.ModelType
			isDefault = false
			steps = append(steps, resolutionStep{
				Step:   "model_routing",
				Lookup: fmt.Sprintf("models[%q]", specialty),
				Found:  resolvedProvider + " / " + resolvedModel,
				Status: "hit",
			})
		} else {
			steps = append(steps, resolutionStep{
				Step:     "model_routing",
				Lookup:   fmt.Sprintf("models[%q]", specialty),
				Found:    "",
				Status:   "miss",
				Fallback: resolvedProvider + " / " + resolvedModel + " (default)",
			})
		}
	} else {
		steps = append(steps, resolutionStep{
			Step:     "model_routing",
			Lookup:   "(no specialty)",
			Found:    "",
			Status:   "miss",
			Fallback: resolvedProvider + " / " + resolvedModel + " (default)",
		})
	}

	result.Steps = steps
	result.ResolvedProvider = resolvedProvider
	result.ResolvedModel = resolvedModel
	result.ResolvedCtxWindow = provider.EffectiveContextWindow(resolvedModel, cfg.GetContextWindowTokens())
	result.IsDefault = isDefault
	return result
}

// findAgentPathPrefix returns the index of "agents-builtin/" or "agents/" in path,
// or -1 if not found.
func findAgentPathPrefix(path string) int {
	for _, prefix := range []string{"agents-builtin" + string(filepath.Separator), "agents" + string(filepath.Separator)} {
		for i := len(path) - 1; i >= 0; i-- {
			if i+len(prefix) <= len(path) && path[i:i+len(prefix)] == prefix {
				return i
			}
		}
	}
	return -1
}
