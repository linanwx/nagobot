package tools

import "github.com/linanwx/nagobot/prompts"

// Model-facing prose owned by this package. The text lives in
// prompts/templates/<key>.md; see package prompts.

// Tool definitions: description plus parameter descriptions. The schema
// structure (types, enums, required) stays in each Def().
var (
	// dispatch and check_session name the targets that take a task_id. That
	// phrase is DERIVED (targetsAccepting) and filled in as TASK_ID_TARGETS,
	// never written into the file: it sits in the cached tool prefix, and a
	// stale target name there is shown to every model on every turn while
	// nothing rejects it.
	promptToolDispatch     = prompts.DeclareTool("dispatch", prompts.WithVars("TASK_ID_TARGETS"))
	promptToolCheckSession = prompts.DeclareTool("check_session", prompts.WithVars("TASK_ID_TARGETS"))
	promptToolExec         = prompts.DeclareTool("exec")
	promptToolReadFile     = prompts.DeclareTool("read_file")
	promptToolWriteFile    = prompts.DeclareTool("write_file")
	promptToolEditFile     = prompts.DeclareTool("edit_file")
	promptToolGrep         = prompts.DeclareTool("grep")
	promptToolGlob         = prompts.DeclareTool("glob")
	promptToolHealth       = prompts.DeclareTool("health")
	promptToolUseSkill     = prompts.DeclareTool("use_skill")
	promptToolWebSearch    = prompts.DeclareTool("web_search")
	promptToolWebFetch     = prompts.DeclareTool("web_fetch")
)

// Guidance carried in tool results.
var (
	promptReadImageUnsupported = prompts.Declare("tool-results/read-file-image-unsupported", prompts.OneLine())
	promptReadImageDelegate    = prompts.Declare("tool-results/read-file-image-delegate", prompts.WithVars("PATH"))
	promptReadAudioUnsupported = prompts.Declare("tool-results/read-file-audio-unsupported", prompts.OneLine())
	promptReadAudioDelegate    = prompts.Declare("tool-results/read-file-audio-delegate", prompts.OneLine())
	promptReadPDF              = prompts.Declare("tool-results/read-file-pdf", prompts.OneLine())

	promptEditNotFoundContents = prompts.Declare("tool-results/edit-file-not-found-contents", prompts.OneLine(), prompts.WithVars("LINES"))
	promptEditNotFoundRecipe   = prompts.Declare("tool-results/edit-file-not-found-recipe", prompts.OneLine(), prompts.WithVars("LINES", "PATH"))

	promptExecConfirmRm  = prompts.Declare("tool-results/exec-confirm-rm", prompts.OneLine(), prompts.WithVars("TOKEN"))
	promptExecConfirmPip = prompts.Declare("tool-results/exec-confirm-pip", prompts.OneLine(), prompts.WithVars("TOKEN"))

	promptDispatchTurnContinues = prompts.Declare("tool-results/dispatch-turn-continues", prompts.OneLine())
	promptDispatchBatchedEmpty  = prompts.Declare("tool-results/dispatch-batched-empty", prompts.OneLine())

	promptCheckSessionMissing  = prompts.Declare("tool-results/check-session-missing", prompts.OneLine())
	promptCheckSessionRunning  = prompts.Declare("tool-results/check-session-running", prompts.OneLine())
	promptCheckSessionPending  = prompts.Declare("tool-results/check-session-pending", prompts.OneLine())
	promptCheckSessionIdle     = prompts.Declare("tool-results/check-session-idle", prompts.OneLine())
	promptCheckSessionUnloaded = prompts.Declare("tool-results/check-session-unloaded", prompts.OneLine())

	promptWebFetchFileReadable   = prompts.Declare("tool-results/web-fetch-file-readable", prompts.OneLine())
	promptWebFetchFileUnreadable = prompts.Declare("tool-results/web-fetch-file-unreadable", prompts.OneLine())
)
