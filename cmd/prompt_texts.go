package cmd

import "github.com/linanwx/nagobot/prompts"

// Model-facing prose owned by this package. The text lives in
// prompts/templates/<key>.md; see package prompts.
var (
	// `delivery` labels of cron messages routed through the dispatcher.
	promptCronCallerTarget = prompts.Declare("delivery/cron-caller-target", prompts.OneLine(), prompts.WithVars("TARGET"))
	promptCronCallerNone   = prompts.Declare("delivery/cron-caller-none", prompts.OneLine())

	// Closing line of a heartbeat wake, naming the selected task's skill.
	promptHeartbeatDream   = prompts.Declare("notices/heartbeat-dream", prompts.OneLine())
	promptHeartbeatReflect = prompts.Declare("notices/heartbeat-reflect", prompts.OneLine())
	promptHeartbeatNone    = prompts.Declare("notices/heartbeat-none", prompts.OneLine())
)
