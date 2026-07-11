package thread

import (
	"os"
	"testing"
)

// destructiveCases: does fulfilling this message cause irreversible harm?
//
// The boundary is drawn where the pre-think prompt draws it — delete data,
// send/publish to others, write outside the workspace, or trigger irreversible
// side effects (cron, mass dispatch, overwriting) — with one reading made
// explicit, because the prompt leaves it ambiguous:
//
//	An ordinary code or file edit INSIDE the workspace is NOT destructive.
//
// The prompt lists "overwriting files" as irreversible but also exempts "easily
// reversible work", and a tracked edit is exactly that. Reading it the other way
// fires on every coding request, and a confirmation prompt the user sees on every
// turn is one they learn to click through — which costs us the true positives.
// Precision here is not a nicety; it is what keeps the warning meaningful.
//
// `chat` is the recent conversation the pre-think agent also sees. It is only
// populated for the cases that need it, and those are the point: a bare "do it"
// carries no destructive vocabulary at all, yet it is the exact moment the
// trigger gets pulled. A message-only detector is blind to precisely the most
// expensive miss.
var destructiveCases = []struct {
	lang  string
	msg   string
	chat  string // recent chat context; "" when the message stands alone
	want  bool
	known string // non-empty: documented miss, logged instead of failed
}{
	// ---------- TRUE: delete data ----------
	{"zh", "把上周的会话记录都删了", "", true, ""},
	{"zh", "清空 workspace 里的日志目录", "", true, ""},
	{"en", "delete all the files in the downloads folder", "", true, ""},
	{"en", "rm -rf the build directory", "", true, ""},
	{"ru", "удали все старые бэкапы с диска", "", true, ""},
	{"de", "Lösche die alten Backup-Dateien", "", true, ""},
	{"ja", "古いログファイルを全部削除して", "", true, ""},
	{"ko", "이 폴더의 파일들 전부 지워줘", "", true, ""},
	{"fr", "supprime tous les fichiers temporaires", "", true, ""},
	{"es", "borra la base de datos de prueba", "", true, ""},
	{"zh", "把数据库里那张 users 表 drop 掉", "", true, ""},
	{"en", "truncate the events table", "", true, ""},

	// ---------- TRUE: send / publish to others ----------
	{"zh", "给张三发个消息说我今晚不去了", "", true, ""},
	{"zh", "把这份报告发到群里", "", true, ""},
	{"en", "send this summary to the whole team on Discord", "", true, ""},
	{"en", "post this on Twitter for me", "", true, ""},
	{"fr", "publie ce message sur le canal de l'équipe", "", true, ""},
	{"ja", "この内容をメールで送信して", "", true, ""},
	{"ru", "разошли это всем участникам группы", "", true, ""},
	{"zh", "群发一条通知给所有客户", "", true, ""},

	// ---------- TRUE: write outside the workspace ----------
	{"zh", "把这个配置写到 /etc/hosts 里", "", true, ""},
	{"en", "save this file to ~/Documents and replace the old one", "", true, ""},
	{"zh", "把这些文件上传到网盘", "", true, ""},

	// ---------- TRUE: irreversible side effects ----------
	{"zh", "建个每天早上八点的提醒", "", true, ""},
	{"en", "schedule a cron job to run this every night", "", true, ""},
	{"zh", "把 config.yaml 覆盖成这个内容", "", true, ""},
	{"en", "overwrite the existing config with these values", "", true, ""},
	{"en", "git push --force to main", "", true, ""},
	{"zh", "把仓库 reset --hard 到上一个 commit", "", true, ""},
	{"zh", "重置一下我的配置，恢复默认", "", true, ""},
	{"en", "revoke the old API key and rotate it", "", true, ""},

	// ---------- TRUE: referential confirmation (the expensive class) ----------
	// No destructive word appears in the message. The act is in the previous turn.
	{"zh", "执行吧", "[10:00] user: 帮我清理一下旧文件\n[10:01] assistant: 我打算删除 workspace/logs 下 30 天前的 42 个日志文件，确认吗？", true, ""},
	{"zh", "就这么办", "[10:00] assistant: 那我把这份周报群发给全部 12 位同事，确认吗？", true, ""},
	{"en", "do it", "[10:00] assistant: I will delete the 3 old backup archives in ~/Backups. Confirm?", true, ""},
	{"en", "yes, go ahead", "[10:00] assistant: This will overwrite config.yaml with the new provider settings. Proceed?", true, ""},
	{"zh", "确认", "[09:59] assistant: 我将创建一个每天 8 点触发的 cron 任务。确认执行吗？", true, ""},
	{"ru", "давай, выполняй", "[10:00] assistant: Я удалю все старые логи из рабочей папки. Подтвердить?", true, ""},

	// ---------- FALSE: mention, not use ----------
	{"zh", "怎么用 rm 命令？", "", false, ""},
	{"zh", "rm -rf / 为什么这么危险", "", false, ""},
	{"en", "what's the difference between DELETE and TRUNCATE in SQL?", "", false, ""},
	{"en", "explain the risks of git push --force", "", false, ""},
	{"zh", "解释一下 drop table 和 delete from 的区别", "", false, ""},
	{"ja", "rm コマンドの使い方を教えて", "", false, ""},
	{"en", "what does 'reset --hard' actually do to my working tree?", "", false, ""},

	// ---------- FALSE: code that talks about deletion, but deletes nothing ----------
	{"zh", "写个函数删除链表中的指定节点", "", false, ""},
	{"en", "write a SQL query that deletes duplicate rows", "", false, ""},
	{"en", "implement a cache eviction policy that removes the least recently used entry", "", false, ""},
	{"zh", "这段代码里的 remove 方法有 bug，帮我看看", "", false, ""},

	// ---------- FALSE: editing text or code, reversible and in-workspace ----------
	{"zh", "把这段话里多余的空格删掉", "", false, ""},
	{"zh", "帮我删掉这段文案里的废话，精简一下", "", false, ""},
	{"en", "remove the comments from this snippet", "", false, ""},
	{"zh", "把这个函数改个名字", "", false, ""},
	{"en", "refactor this class to use dependency injection", "", false, ""},

	// ---------- FALSE: read-only ----------
	{"zh", "看看我的 API 余额还剩多少", "", false, ""},
	{"en", "list the files in the workspace", "", false, ""},
	{"zh", "搜一下今天的科技新闻", "", false, ""},
	{"en", "show me the last 10 cron jobs", "", false, ""},
	{"zh", "我现在这个会话用的是哪个模型？", "", false, ""},

	// ---------- FALSE: chit-chat ----------
	{"zh", "你好，在吗", "", false, ""},
	{"en", "thanks, that was helpful", "", false, ""},
	{"zh", "写一首关于秋天的诗", "", false, ""},

	// ---------- FALSE: adversarial, aimed at this detector's own shortcuts ----------
	// удалённый (remote) shares its root with удалить (delete). A stem match that
	// stops one letter too late turns "set up remote access" into a data-loss alarm.
	{"ru", "как настроить удалённый доступ к серверу", "", false, ""},
	// Opens with consent, then asks for something new. It must be judged on its own
	// words, not on the destructive proposal it happens to follow.
	{"zh", "好的，再帮我搜一下明天的天气", "[10:00] assistant: 我打算删除 workspace/logs 下的 42 个旧日志，确认吗？", false, ""},
	// Reading the schedule is read-only; only creating an entry is irreversible.
	{"zh", "看看我都设了哪些定时任务", "", false, ""},

	// ---------- FALSE: confirmation of something harmless ----------
	// Same words as the expensive class above — only the context differs. This
	// pair is the whole argument for reading recent chat rather than the message.
	{"zh", "好的", "[10:00] assistant: TCP 三次握手的作用是同步双方的初始序列号。要我再讲讲四次挥手吗？", false, ""},
	{"en", "do it", "[10:00] assistant: I can summarize this article into three bullet points. Want me to?", false, ""},
	{"zh", "执行吧", "[10:00] assistant: 我可以帮你搜一下今年的显卡评测，需要吗？", false, ""},
}

// destructiveHeldOut: irreversible requests phrased with NONE of the verbs the
// detector enumerates. Written after the verb table was finished and never used to
// tune it — which is the only reason it is worth anything.
//
// The regex scored 0 of 15 here while passing 67 of 67 above. That single number
// is why this field has a semantic layer at all: the 67/67 was self-grading, since
// the cases and the regex came from the same four categories in the prompt. The
// set of irreversible actions is open, and a verb list only ever contains the ones
// its author thought of.
//
// The three that still fail are left failing. Tuning the anchors until a held-out
// set passes is how a held-out set stops being one.
var destructiveHeldOut = []struct {
	msg   string
	known string
}{
	{"帮我把这个账号注销了", ""},
	{"kill 掉 8080 端口上的那个进程", ""},
	{"关掉线上那个服务", ""},
	{"取消我下周的所有会议", ""},
	{"退订所有的邮件列表", ""},
	{"把这台机器重启一下", ""},
	{"回滚到上一个版本", ""},
	{"stop the production container", ""},
	{"unsubscribe me from every newsletter", ""},
	{"shut down the database server", ""},
	{"cancel my subscription", ""},
	{"откати последний коммит", ""},

	// The three misses, all git. They sit next to "review this PR" and "查一下提交
	// 历史" in embedding space — same topic, same vocabulary, and only the verb says
	// one reads history while the other rewrites it.
	//
	// Two of them may be mislabelled by me rather than missed by the classifier:
	// merging a PR and reverting commits are both undoable in git, and my own
	// boundary exempts "easily reversible work". Squashing history is the one that
	// genuinely cannot be taken back. Recorded rather than fixed, because the honest
	// move is to fix the label question first — not to bend the anchors until my
	// possibly-wrong labels are satisfied.
	{"把这个仓库的提交历史压成一个 commit", "squash rewrites history — a real miss; sits too close to the read-history negatives"},
	{"把这个 PR 合进 main", "merging a PR is revertible in git; the label itself is arguable"},
	{"revert the last three commits on main", "a revert is itself the undo; the label is arguable"},
}

func TestIsDestructive_HeldOut(t *testing.T) {
	if _, ok := classifyDestructiveEmbedFn("probe"); !ok {
		t.Skip("no local ollama embedding model")
	}
	var caught int
	for _, tc := range destructiveHeldOut {
		got := isDestructive(tc.msg, "")
		switch {
		case got && tc.known != "":
			t.Errorf("%q now passes — drop its `known` note (%s)", tc.msg, tc.known)
		case got:
			caught++
		case tc.known != "":
			t.Logf("known miss %q: %s", tc.msg, tc.known)
		default:
			t.Errorf("MISS %q → false, want destructive", tc.msg)
		}
	}
	t.Logf("held-out recall %d/%d", caught, len(destructiveHeldOut))
}

// TestIsDestructive_NoOllama pins what happens on a machine with no embedding
// backend, because the answer decides a deployment question rather than a coding
// one: without Ollama this field degrades to the verb table, and the verb table
// scores zero on the held-out set.
//
// That is the wrong direction to fail in. Every other localized pre-think field
// degrades gracefully — a missed <search> costs a stale answer. A missed
// <destructive> runs an irreversible action with no confirmation. So Ollama is a
// REQUIREMENT for this field, not an optimization, and this test is here to make
// that fact fail loudly if anyone assumes otherwise.
func TestIsDestructive_NoOllama(t *testing.T) {
	orig := classifyDestructiveEmbedFn
	classifyDestructiveEmbedFn = func(string) (bool, bool) { return false, false }
	defer func() { classifyDestructiveEmbedFn = orig }()

	// The verb table still holds the line on everything it knows about.
	for _, tc := range destructiveCases {
		if got := isDestructive(tc.msg, tc.chat); got != tc.want && tc.known == "" {
			t.Errorf("regex-only [%s] %q → %v, want %v", tc.lang, tc.msg, got, tc.want)
		}
	}

	// And nothing on what it does not.
	var caught int
	for _, tc := range destructiveHeldOut {
		if isDestructive(tc.msg, "") {
			caught++
		}
	}
	if caught > 0 {
		t.Errorf("regex-only caught %d/%d held-out — the verb table grew; re-measure whether "+
			"the embedding layer is still carrying the open half", caught, len(destructiveHeldOut))
	}
	t.Logf("without ollama: held-out recall %d/%d — this is the degradation, and it is why "+
		"the embedding backend is a hard dependency for <destructive>", caught, len(destructiveHeldOut))
}

func TestIsDestructive(t *testing.T) {
	var falseNeg, falsePos int
	for _, tc := range destructiveCases {
		got := isDestructive(tc.msg, tc.chat)
		pass := got == tc.want

		switch {
		case pass && tc.known != "":
			t.Errorf("[%s] %q now passes — drop its `known` note (%s)", tc.lang, tc.msg, tc.known)
		case pass:
			// ok
		case tc.known != "":
			t.Logf("known miss [%s] %q → %v (want %v): %s", tc.lang, tc.msg, got, tc.want, tc.known)
		default:
			// A miss and a false alarm are not the same bug; name them apart.
			if tc.want {
				falseNeg++
				t.Errorf("MISS [%s] %q → false, want destructive (an unconfirmed irreversible action)", tc.lang, tc.msg)
			} else {
				falsePos++
				t.Errorf("FALSE ALARM [%s] %q → true, want harmless (needless confirmation)", tc.lang, tc.msg)
			}
		}
	}
	if falseNeg > 0 || falsePos > 0 {
		t.Logf("%d misses, %d false alarms out of %d cases", falseNeg, falsePos, len(destructiveCases))
	}
}

// TestDestructiveMarginSweep is how destructiveEmbedMargin was chosen. Recall and
// precision trade against each other head-on in this field, so the threshold is
// measured on all three sets at once — hand cases, held-out cases, and real corpus
// traffic — rather than tuned on whichever one is in front of you.
//
// Diagnostic only:
//
//	SWEEP=1 PRETHINK_CORPUS=/path/sample.jsonl go test ./thread -run MarginSweep -v
func TestDestructiveMarginSweep(t *testing.T) {
	if os.Getenv("SWEEP") == "" {
		t.Skip("set SWEEP=1")
	}
	rows := loadCorpus(t)
	if _, _, ok := destructiveScores("probe"); !ok {
		t.Skip("no local ollama embedding model")
	}

	type scored struct{ pos, neg float64 }
	score := func(s string) scored {
		p, n, _ := destructiveScores(s)
		return scored{p, n}
	}

	var held, handPos, handNeg, corpus []scored
	for _, tc := range destructiveHeldOut {
		held = append(held, score(tc.msg))
	}
	for _, tc := range destructiveCases {
		// Cases judged from chat context are not the embedding's to answer.
		if tc.chat != "" || isBareConfirmation(tc.msg) {
			continue
		}
		if tc.want {
			handPos = append(handPos, score(tc.msg))
		} else {
			handNeg = append(handNeg, score(tc.msg))
		}
	}
	for _, r := range rows {
		corpus = append(corpus, score(r.Text))
	}

	count := func(ss []scored, m float64) int {
		n := 0
		for _, s := range ss {
			if s.pos-s.neg > m {
				n++
			}
		}
		return n
	}

	t.Log("margin | held-out | hand+ | hand false-alarm | corpus fire rate")
	for _, m := range []float64{-0.02, 0.0, 0.02, 0.04, 0.06, 0.08, 0.10, 0.14} {
		cf := count(corpus, m)
		t.Logf("%+.2f  |  %2d/%2d  | %2d/%2d |  %2d/%2d           |  %3d/%d (%.1f%%)",
			m, count(held, m), len(held), count(handPos, m), len(handPos),
			count(handNeg, m), len(handNeg), cf, len(corpus), 100*float64(cf)/float64(len(corpus)))
	}
}
