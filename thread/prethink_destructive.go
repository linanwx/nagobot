package thread

import (
	"context"
	"regexp"
	"strings"
)

// <destructive> is not like the other pre-think fields. The others ask what the
// message MEANS; this one asks what fulfilling it would DO, and the evidence for
// that is often not in the message at all:
//
//	user: 执行吧
//
// Nothing in those two characters is dangerous. What makes them dangerous is the
// turn before, where the assistant offered to delete 42 files. The pre-think
// agent is handed the last 16 chat lines precisely so it can see that, so the
// local detector gets them too — a message-only version would be blind to the
// single most expensive miss in the whole field, the moment the trigger is pulled.
//
// Hence the shape: fire if the message is destructive on its own, OR if it is a
// bare confirmation of a destructive proposal. The two paths union, which biases
// toward recall — the right bias, because a miss executes an irreversible action
// unconfirmed.
//
// But recall is not free, and this is the part worth being careful about. Every
// false alarm trains the user to click through the confirmation, and a user who
// clicks through by reflex is unprotected on the one turn that mattered. So the
// detector spends most of its rules on NOT firing: on the difference between
// asking about rm and running it, between writing code that deletes a linked-list
// node and deleting data, between editing a paragraph and erasing a disk.
func isDestructive(ctx context.Context, msg, recentChat string) bool {
	return isDestructiveWith(ctx, msg, recentChat, classifyDestructiveEmbedFn)
}

// isDestructiveRegex is the same detector with the embedding layer taken away —
// the verdict preThinkAction falls back on when the embedding classifiers blow
// their wall-clock budget.
//
// It is a weak detector on its own (0 of 15 on the held-out set; see
// prethink_destructive_embed.go), but weak is not the same as wrong-direction. On
// a timeout the alternative is to report false, and false here means an
// irreversible action proceeds with no confirmation. Whatever the verb table does
// know, we keep.
func isDestructiveRegex(msg, recentChat string) bool {
	return isDestructiveWith(context.Background(), msg, recentChat, noEmbed)
}

func isDestructiveWith(ctx context.Context, msg, recentChat string, classify embedClassifier) bool {
	return destructiveIntent(ctx, destructiveSubject(msg, recentChat), classify)
}

// destructiveSubject is the text this classifier actually judges — the only
// place in pre-think where that differs from the user's message.
//
// A bare confirmation is judged ONLY by what it confirms, never on its own. The
// order matters and it is not obvious: running the detector on "do it" first
// calls it destructive every time, because in isolation "do it" reads as an
// imperative to go act — the semantic classifier has nothing else to go on. The
// whole point of the class is that the words carry no intent; the intent is in
// the turn above. So we never let them be judged alone.
//
// It is exported to localPreThink (rather than inlined above) so the shared
// query embedder can prefetch this exact text. Two definitions of "what does
// destructive embed" would mean the prefetch silently missed and the classifier
// spent a second round trip.
func destructiveSubject(msg, recentChat string) string {
	if isBareConfirmation(msg) {
		return lastAssistantTurn(recentChat)
	}
	return msg
}

// ---------------------------------------------------------------------------
// Exemptions — checked before any destructive verb is allowed to fire.
// ---------------------------------------------------------------------------

// conceptualRE marks a question ABOUT a dangerous thing rather than a request TO
// do it. Both halves are required: a question frame AND a topic that is clearly a
// command or language construct rather than the user's data.
//
// Requiring the topic half is what keeps "怎么删掉这个文件" (a real request — the
// object is the user's file) apart from "怎么用 rm 命令" (a lesson — the object is
// the command). A question frame alone would collapse the two and silently drop a
// genuine deletion, so the frame is necessary but never sufficient.
var (
	conceptualFrameRE = regexp.MustCompile(`(?i)怎么用|如何使用|的用法|使い方|是什么意思|什么意思|有什么区别|的区别|区别是什么|解释一下|讲一下|讲讲|教我|为什么|原理|风险` +
		`|how (?:do|does|to) |what (?:is|are|does|do) |difference between|\bexplain\b|risks? of|when should i|why is .* dangerous`)
	// A command, flag, or language construct — i.e. the thing being asked about is
	// a tool, not the user's data.
	conceptualTopicRE = regexp.MustCompile(`(?i)命令|指令|语句|参数|コマンド|명령` +
		`|\bcommand\b|\bstatement\b|\bflag\b|\bsyntax\b|\boption\b` +
		`|rm\s+-[a-z]|drop\s+table|delete\s+from|truncate\b|reset\s+--hard|push\s+--force|--force\b`)
)

// codeArtifactRE marks a request to WRITE code that happens to talk about
// deletion. The output is text; nothing is destroyed. "write a query that deletes
// duplicate rows" produces SQL, it does not run it.
//
// Deliberately narrow: it lists code artifacts (function, query, method, class,
// algorithm) and NOT "script". "写个脚本把这些文件删了" reads as an instruction to
// clean the files, with the script as the means — so it is left to fire. When the
// artifact is ambiguous, recall wins.
// The optional qualifier before the artifact noun is load-bearing: "write a SQL
// query" and "写个 Python 函数" name the language before the thing, and a pattern
// that demands the noun sit right after the article misses both.
var codeArtifactRE = regexp.MustCompile(`(?i)(?:写|实现|做)(?:一)?个?\s*(?:\w+\s*)?(?:函数|方法|查询|算法|类|正则|语句|插件|脚本|程序|应用|机器人|策略)` +
	`|\b(?:write|implement|build|create|make) (?:a |an |me a )?(?:\w+ )?(?:function|method|query|class|algorithm|regex|unit test|plugin|script|program|app|bot|policy|cache)\b` +
	`|(?:напиши|создай|сделай)\s+(?:\w+\s+)?(?:плагин|скрипт|функци|программ|бот|прилож)`)

// textObjectRE marks deletion applied to a passage of text or a code snippet the
// user is working on — copy-editing, not data loss. "删掉这段文案里的废话" and
// "remove the comments from this snippet" rewrite content; they destroy nothing.
var textObjectRE = regexp.MustCompile(`(?i)这段(?:话|文字|文案|代码|内容)|这篇|这句|段落|文案|标点|空格|注释|废话` +
	`|\bthis (?:text|paragraph|passage|snippet|sentence|draft)\b|\bthe comments\b|\bwhitespace\b|\bfrom this (?:code|snippet)\b`)

// ---------------------------------------------------------------------------
// Destructive intent.
// ---------------------------------------------------------------------------

// The four categories the pre-think prompt names, kept apart so a false positive
// can be traced to the rule that caused it.
//
// No \b before or after a CJK token: in Go's RE2 \b is ASCII-only and never
// matches next to a Han/Kana/Hangul character, so `\b删除\b` is silently dead.
var (
	// 1. Delete or destroy data.
	deleteRE = regexp.MustCompile(`(?i)删除|删掉|删了|删除掉|清空|清除|清理掉|移除|抹掉|擦除|格式化|恢复出厂|重置` +
		`|削除|消して|삭제|지워|지우` +
		`|\bdelete\b|\bdeleting\b|\bdelete[sd]\b|\bremove\b|\bremoving\b|\bwipe\b|\berase\b|\bpurge\b|\bdrop\b|\btruncate\b|\bunlink\b|\brmdir\b|\brm\b` +
		// Inflected languages need stems, not surface forms: the assistant proposes
		// "я удалю" (I will delete) while the user commands "удали" (delete!), and a
		// literal list of one form misses the other. But the stem must stop short of
		// two neighbours that mean something else entirely:
		//   удалённый = REMOTE (same root) — "настроить удалённый доступ" is not deletion
		//   удаление  = DELETION, the noun — "о невозможности удаления пунктов" is a
		//               letter ABOUT deletion, so the -ени- nominalization is excluded.
		`|удал(?:и|ю|ит|я)|очист|сотри` +
		// \belimina\b is exact rather than a stem because Spanish "elimina" is a
		// prefix of English "eliminate", and the corpus caught it firing on an
		// English article that merely used the word.
		`|\blösch|\bentfern|\bsupprim|\befface|\bborra|\belimina(?:r)?\b`)

	// 2. Send or publish to others — irreversible the instant it leaves.
	//
	// The object list is explicit. "\bsend .{0,30}\bto\b" was not: it fired on
	// "which day has the best exchange rate to send money from usa to india", a
	// pure question about rates. Sending is only destructive when there is content
	// and a recipient, so the pattern insists on content.
	//
	// Russian takes imperatives only (отправь / разошли), not the infinitive. In the
	// corpus, "необходимо отправить описание" means "I have to submit a description"
	// — the user narrating their own task before asking us to write it, not asking
	// us to send anything.
	sendRE = regexp.MustCompile(`(?i)发给|发送给|发到|发一条|发个消息|转发|群发|推送给|发布到|发布一|分享给|上传到|投递` +
		`|送信|送って|보내` +
		`|\bsend (?:this|it|that|them|these|those|out)\b` +
		`|\bsend (?:me |us |him |her |them )?(?:the |a |an |my )?(?:file|files|report|message|msg|email|mail|link|doc|document|summary|photo|image|picture|pdf|notification)\b` +
		`|\bforward (?:this|it|the)\b|\bpublish\b|\bpost (?:this|it|that) (?:on|to)\b|\bbroadcast\b|\bemail (?:this|it|the)\b` +
		`|отправь|отправьте|разошли|\bpublie\b|\benvoie\b|\benvía\b`)

	// 3. Write outside the workspace — beyond the blast radius we control.
	outsideRE = regexp.MustCompile(`(?i)/etc/|/usr/|/var/|/System/|~/Documents|~/Desktop|~/Downloads|网盘|云盘|云端硬盘` +
		`|\bonedrive\b|\bdropbox\b|\bgoogle drive\b|\bicloud\b|\bs3 bucket\b`)

	// 4. Other irreversible side effects: scheduling, overwriting, credential
	//    rotation, force-pushing history.
	//
	// "cron" and "定时任务" are nouns, not verbs, and both are only ever reached
	// through a creation verb here. Listing either bare word fired on "show me the
	// last 10 cron jobs" and "看看我都设了哪些定时任务" — reading the schedule is as
	// read-only as it gets, and a confirmation prompt in front of a list is exactly
	// the noise that teaches a user to stop reading confirmations at all.
	sideEffectRE = regexp.MustCompile(`(?i)覆盖|复写|提醒我|每天.{0,8}(?:点|提醒)|每周.{0,8}(?:提醒|执行)` +
		`|(?:创建|新建|建个|建一个|加个|添加|设个|设一个|配置)\s*[^，。,.]{0,14}(?:定时|提醒|任务|cron)` +
		`|\boverwrite\b|\breplace the (?:old|existing)\b|\brevoke\b|\brotate (?:the |my )?(?:key|token|credential)` +
		`|\b(?:create|add|set ?up|schedule)\b[^,.]{0,20}\bcron\b|\bschedule (?:a|this|it)\b` +
		`|reset\s+--hard|push\s+--force|--force\b|\bforce[- ]push`)
)

// dangerCommandRE lists commands that are destructive as literals, wherever they
// appear. Unlike the verb tables these are unambiguous — no ordinary source file
// being debugged contains "rm -rf" or "DROP TABLE" by accident — so they survive
// the code-paste gate below. Pasting a shell command IS how a user asks for it to
// be run.
var dangerCommandRE = regexp.MustCompile(`(?i)\brm\s+-[a-z]*[rf]|\brmdir\b|\bdrop\s+(?:table|database|schema)\b|\btruncate\s+table\b|\bdelete\s+from\b` +
	`|git\s+push\s+.*--force|push\s+--force|reset\s+--hard|\bmkfs\b|\bdd\s+if=|>\s*/dev/(?:sd|nvme)|format\s+c:`)

// execIntentRE marks a request to RUN the pasted code rather than read it. It is
// the one thing that re-arms the verb table on a paste.
var execIntentRE = regexp.MustCompile(`(?i)跑一下|跑跑|运行|执行|部署|上线|帮我跑` +
	`|\brun (?:this|it|the|that)\b|\bexecute\b|\bdeploy\b|\bapply (?:this|it)\b`)

// destructiveIntent decides whether acting on this text would cause irreversible
// harm. Exemptions run first: a message that is asking about a command, writing
// code, or pasting a stack trace never reaches the verb table, no matter how many
// dangerous words it contains.
func destructiveIntent(ctx context.Context, s string, classify embedClassifier) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	// "怎么用 rm 命令" — a lesson, not an order.
	if conceptualFrameRE.MatchString(s) && conceptualTopicRE.MatchString(s) {
		return false
	}
	// "写个函数删除链表节点" — produces text, destroys nothing.
	if codeArtifactRE.MatchString(s) {
		return false
	}

	// An explicit destructive command outranks everything below, paste or not.
	if dangerCommandRE.MatchString(s) {
		return true
	}

	// A code paste is inert. Eight of the corpus's seventeen alarms were someone
	// pasting a broken app.js and asking why it crashed — the word "Remove" was an
	// identifier inside their own source, not an instruction to us. Debugging code
	// that deletes things deletes nothing; running it does. So on a paste the verb
	// table only re-arms when the user actually asks us to run the thing.
	//
	// (The corpus cannot vouch for this rule's recall half — WildChat users had no
	// tools, so nobody there pastes a script to be executed. nagobot users do, which
	// is exactly why the exec escape hatch and dangerCommandRE exist above it.)
	if codePasteRE.MatchString(s) && !execIntentRE.MatchString(s) {
		return false
	}

	leavesTheMachine := sendRE.MatchString(s) || outsideRE.MatchString(s) || sideEffectRE.MatchString(s)

	// Copy-editing: the object is a passage or a snippet the user is working on, and
	// nothing is going anywhere. "删掉这段文案里的废话" and "remove the comments from
	// this snippet" rewrite content; they destroy nothing.
	//
	// This has to be a gate rather than a branch of the delete rule, because the
	// semantic layer below fires on it too — "delete the extra spaces" looks like a
	// deletion to an embedding. But it must not be an unconditional veto: "把这段代码
	// 发给客户" also names a text object, and sending it to a client is exactly the
	// irreversible act we exist to catch. So the exemption only holds while nothing
	// leaves the machine.
	if textObjectRE.MatchString(s) && !leavesTheMachine {
		return false
	}

	if leavesTheMachine {
		return true
	}
	if deleteRE.MatchString(s) {
		return true
	}

	// Nothing in the verb table matched — which says very little, because the table
	// only knows the irreversible actions I thought to list. Ask meaning. See
	// prethink_destructive_embed.go: the table missed 16 of 16 held-out phrasings,
	// so this is not a garnish on the regex, it is the half that generalizes.
	//
	// No embedding backend → ok=false → we keep the regex verdict. That fails toward
	// fewer confirmations, which is the wrong direction for this field and is the
	// reason an embedding backend is a hard requirement here rather than an optimization.
	if verdict, ok := classify(ctx, s); ok {
		return verdict
	}
	return false
}

// ---------------------------------------------------------------------------
// Confirmation of a prior proposal.
// ---------------------------------------------------------------------------

// confirmWord is one token of consent. Consent is routinely stacked — "yes, go
// ahead", "давай, выполняй", "好的，就这么办" — so the message-level pattern below
// allows a run of them joined by punctuation. Matching only a single word dropped
// both of the two-word forms above, which is the wrong half to be strict about.
const confirmWord = `好的|好呀|好|行吧|行|可以|没问题|确认|同意|执行吧|执行|就这么办|就这样|这样就行|来吧|干吧|上吧|继续|开始吧|动手吧|去做吧|办吧` +
	`|ok|okay|k|yes|yep|yeah|yup|sure|please do|do it|go ahead|proceed|confirm(?:ed)?|approved|sounds good|let'?s do it|go for it` +
	`|давай|выполняй|подтверждаю|согласен|да` +
	`|はい|お願いします|やって|実行して` +
	`|네|응|해줘` +
	`|ja|mach das|bitte|oui|vas-y|sí|dale|adelante`

// bareConfirmRE matches a message whose ENTIRE content is consent. The anchors do
// the real work: "好的" consents to the previous turn, while "好的，再帮我搜一下天气"
// merely opens politely and is a new request — it must not inherit the danger of
// whatever the assistant proposed before it.
var bareConfirmRE = regexp.MustCompile(`(?i)^[\s,，。.!！~]*(?:` + confirmWord + `)` +
	`(?:[\s,，、]+(?:` + confirmWord + `))*[\s,，。.!！~]*$`)

// confirmMaxRunes caps how long a "bare" confirmation may be. A short consent
// carries no information of its own, which is exactly why we look backward; a
// long message carries its own intent and was already judged on it.
const confirmMaxRunes = 24

func isBareConfirmation(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" || len([]rune(msg)) > confirmMaxRunes {
		return false
	}
	return bareConfirmRE.MatchString(msg)
}

// assistantTurnRE pulls the assistant lines out of the flat transcript
// ReadRecentChat produces ("[10:04] assistant: ...", one entry per line).
var assistantTurnRE = regexp.MustCompile(`(?m)^(?:\[[^\]]*\]\s*)?assistant:\s*(.*)$`)

// lastAssistantTurn returns the most recent thing the assistant said — the
// proposal a bare confirmation is answering. Empty when there is nothing to
// confirm, in which case the confirmation is harmless by construction.
func lastAssistantTurn(recentChat string) string {
	ms := assistantTurnRE.FindAllStringSubmatch(recentChat, -1)
	if len(ms) == 0 {
		return ""
	}
	return strings.TrimSpace(ms[len(ms)-1][1])
}
