package thread

import "github.com/linanwx/nagobot/prompts"

// Model-facing prose owned by this package. The text lives in
// prompts/templates/<key>.md; see package prompts.

// Wake action hints (the `action` field of a wake's frontmatter). All are one
// line, because a multi-line action would render as a YAML block scalar.
var (
	// promptWakeUser is the action on every channel-user wake: a short reminder
	// of how to talk with the person. The full guidance lives in soul.md's
	// "How You Talk" section; this keeps only the parts that measurably bite on
	// the newest message (read the conversation, name a frustration first,
	// don't restate, short spoken paragraphs with no bold or lists).
	//
	// Why both places: replaying a real kingsley turn on deepseek-flash, the
	// guidance as a system-prompt section alone barely moved the reply (~590 →
	// ~520 chars, bold and lists kept), while on the newest message it took
	// replies to 217–323 chars with no bold and no lists. But every past wake
	// keeps its action until idle Tier-1 compression strips it (wakeTrimKeys),
	// which now waits 60 minutes, so a ~650-token action piled up across an
	// active conversation. Re-measured after the split (4 real turns × 4 runs):
	// section + ~85-token action matched the old full action — 0 bold, 0 list
	// lines, similar length — while no action at all brought bold back.
	//
	// It is written as instructions, not as the books behind them: the same
	// ideas phrased as a summary of Duhigg/Gottman/Voss/MI came back worse than
	// baseline (numbered lists in the prompt reappeared as numbered lists in the
	// reply). "You may agree" (in soul.md) is load-bearing: forbidding
	// confirmation outright pushed the model into contradicting the user's
	// premise instead. The action is self-contained because agents other than
	// soul answer channel users too and do not see soul.md.
	promptWakeUser            = prompts.Declare("wake/user", prompts.OneLine())
	promptWakeSession         = prompts.Declare("wake/session", prompts.OneLine())
	promptWakeCron            = prompts.Declare("wake/cron", prompts.OneLine())
	promptWakeCompression     = prompts.Declare("wake/compression", prompts.OneLine())
	promptWakeHeartbeat       = prompts.Declare("wake/heartbeat", prompts.OneLine())
	promptWakeResume          = prompts.Declare("wake/resume", prompts.OneLine())
	promptWakeAudioPreview    = prompts.Declare("wake/audio-preview", prompts.OneLine())
	promptWakeImagePreview    = prompts.Declare("wake/image-preview", prompts.OneLine())
	promptWakeProgress        = prompts.Declare("wake/progress", prompts.OneLine())
	promptWakeProgressSummary = prompts.Declare("wake/progress-summary", prompts.OneLine())
	promptWakeQuote           = prompts.Declare("wake/quote", prompts.OneLine())
	promptWakeDefault         = prompts.Declare("wake/default", prompts.OneLine())

	promptHistoryInstruction         = prompts.Declare("wake/history-instruction", prompts.OneLine())
	promptHistoryInstructionNoAction = prompts.Declare("wake/history-instruction-no-action", prompts.OneLine())
)

// Notices: text the runtime injects or attaches on its own initiative.
var (
	promptTurnEnded              = prompts.Declare("notices/turn-ended", prompts.OneLine(), prompts.WithVars("NAGOBOT_BIN", "CHILD_SESSION"))
	promptTurnEndedError         = prompts.Declare("notices/turn-ended-error", prompts.OneLine(), prompts.WithVars("ERROR"))
	promptTurnEndedNoSummarizer  = prompts.Declare("notices/turn-ended-no-summarizer", prompts.OneLine())
	promptTurnEndedSummaryFailed = prompts.Declare("notices/turn-ended-summary-failed", prompts.OneLine())
	promptCrossThreadDispatchReq = prompts.Declare("notices/cross-thread-dispatch-required", prompts.WithVars("PEER_SESSION"))
	promptOperatorStop           = prompts.Declare("notices/operator-stop", prompts.OneLine())
	promptPinRequest             = prompts.Declare("notices/pin-request", prompts.WithVars("PINS_DIR", "MESSAGE"))
	promptCompressedWithID       = prompts.Declare("notices/compressed-with-id", prompts.OneLine(), prompts.WithVars("MESSAGE_ID"))
	promptCompressed             = prompts.Declare("notices/compressed", prompts.OneLine())
	promptBalanceSwitch          = prompts.Declare("notices/balance-switch", prompts.OneLine())
)

// Headers of the per-session blocks injected into the system prompt: the
// `prompt:` value of each block's YAML header, so one line.
var (
	promptHeaderUserPreference         = prompts.Declare("headers/user-preference", prompts.OneLine())
	promptHeaderUserPreferenceOverlong = prompts.Declare("headers/user-preference-overlong", prompts.OneLine())
	promptHeaderHeartbeat              = prompts.Declare("headers/heartbeat", prompts.OneLine())
	promptHeaderClarify                = prompts.Declare("headers/clarify", prompts.OneLine())
	promptHeaderClarifyOverlong        = prompts.Declare("headers/clarify-overlong", prompts.OneLine())
	promptHeaderFileTrack              = prompts.Declare("headers/file-track", prompts.OneLine())
	promptHeaderMemoryIndex            = prompts.Declare("headers/memory-index", prompts.OneLine())
)
