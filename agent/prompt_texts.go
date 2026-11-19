package agent

import "github.com/linanwx/nagobot/prompts"

// Headers of the blocks Build() assembles: the `prompt:` value of each block's
// YAML header, so one line. The text lives in prompts/templates/headers/.
var (
	promptHeaderAgentIdentity     = prompts.Declare("headers/agent-identity", prompts.OneLine())
	promptHeaderCoreMechanism     = prompts.Declare("headers/core-mechanism", prompts.OneLine())
	promptHeaderWorldKnowledge    = prompts.Declare("headers/world-knowledge", prompts.OneLine())
	promptHeaderGlobalInstruction = prompts.Declare("headers/global-instruction", prompts.OneLine())
	promptHeaderPeopleKnowledge   = prompts.Declare("headers/people-knowledge", prompts.OneLine())
)
