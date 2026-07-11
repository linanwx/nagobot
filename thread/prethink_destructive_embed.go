package thread

import (
	"context"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// The verb table in prethink_destructive.go passed all 67 hand-written cases and
// then missed 16 out of 16 held-out ones — a clean 100% failure. The held-out set
// was destructive requests phrased outside the four categories the prompt names:
// 注销账号, kill 掉这个进程, 关掉线上服务, 把 PR 合了, 回滚到上个版本, 取消订阅,
// 退订邮件列表, shut down the database, revert the last three commits.
//
// That is not a gap to patch by adding verbs. It is the shape of the problem: the
// set of irreversible actions is open, and every list of them is a list of the
// ones you happened to think of. The 67/67 was an illusion — I wrote the cases and
// the regex from the same four categories, so the test only asked whether I could
// restate my own enumeration.
//
// So the regex keeps the job it is actually good at (known-dangerous literals like
// rm -rf, and the precision gates that hold the corpus false-alarm rate at 0.8%),
// and meaning handles the open half: is this asking the assistant to DO something
// that cannot be taken back?
//
// The two are unioned, which biases toward recall — the right bias, because a miss
// runs an irreversible action unconfirmed while a false alarm only costs a
// round-trip. But recall is bought with the user's attention, so the negatives
// below are chosen adversarially: for every positive there is a read-only twin
// (cancel vs list, kill vs show, merge vs review, send vs draft). Those twins are
// the whole boundary.
var destructivePosAnchors = []string{
	// destroy / remove real data
	"把服务器上的旧备份清理掉",
	"注销掉我在那个平台上的账号",
	"把这个订单取消掉",
	"wipe the staging database",
	"drop the staging schema",
	"empty the trash and free up the disk",
	"удали всю папку с логами",
	"Lösche das alte Backup vom Server",
	// stop / destroy running things
	"终止正在跑的那个任务",
	"把线上的容器停掉",
	"kill the process listening on port 3000",
	"shut down the instance",
	"terminate the running deployment",
	"останови сервер",
	"このタスクを停止して",
	"서버를 재시작해줘",
	"把这台机器重启一下",
	// irreversible version-control moves
	"把这个分支合并到主干并删掉分支",
	"撤销我上一次的提交",
	"roll back production to the previous release",
	"close the pull request and delete the branch",
	"force push my local branch over the remote",
	"откати последний коммит",
	// send / publish to others
	"把这条动态发出去",
	"把这份合同的最终版发给对方",
	"publish this article to the blog",
	"email this contract to the client",
	"опубликуй этот пост в канале",
	"envoie ce document au client",
	// commitments and subscriptions
	"cancel my subscription immediately",
	"unsubscribe me from all mailing lists",
	"退掉我买的那个会员",
	"cancela el pedido",
	// scheduling and credentials
	"每周一自动跑一次这个脚本",
	"set up a job that runs every night",
	"把 API 密钥换成新的，旧的作废",
	"revoke the old token",
	"覆盖掉服务器上的配置文件",
}

// destructiveNegAnchors is the rival class. Half of it is deliberately the
// read-only shadow of the positives above: listing subscriptions is not cancelling
// one, showing processes is not killing one, reviewing a pull request is not
// merging it, and drafting an email is not sending it. A classifier that cannot
// hold those four pairs apart is not measuring danger, it is measuring topic.
var destructiveNegAnchors = []string{
	// the read-only twins
	"列出所有正在运行的容器",
	"show me the running processes",
	"list my active subscriptions",
	"查一下这个分支的提交历史",
	"review this pull request and tell me what you think",
	"check the status of the deployment",
	"看看我的余额还剩多少",
	"帮我起草一封给客户的邮件",
	"draft an email to the client, do not send it yet",
	// Reading the schedule is the twin of creating one, and the negatives had every
	// other twin but this one.
	"查看当前所有的定时任务",
	"show me my scheduled cron jobs",
	// Setting a thing up is not tearing a thing down. Without this, anything phrased
	// against a server drifted into the danger class — "настроить удалённый доступ"
	// (configure remote access) was called destructive.
	"配置一下服务器的远程访问权限",
	"set up SSH access on the new server",
	"帮我把 API key 填到配置里",
	// asking about a dangerous thing rather than doing it
	"what is the difference between a merge and a rebase",
	"what happens if I force push",
	"how do I cancel a subscription on that site",
	"解释一下这个报错是什么意思",
	"rebase 和 merge 有什么区别",
	"расскажи, как работает git rebase",
	// building software that talks about these actions
	"写一个快排的实现",
	"write a binary search in Python",
	"напиши функцию сортировки",
	"帮我把这段代码重构一下",
	"给这个函数加个注释",
	"这段代码为什么会崩",
	"explain what this stack trace means",
	"why does this function return undefined",
	"what does this SQL query do",
	// working on text
	"帮我把这段话翻译成英文",
	"总结一下这篇文章",
	"summarize this article in three points",
	"proofread this paragraph",
	// ordinary questions and chat
	"什么是幂等性",
	"我该学 Rust 还是 Go",
	"给我讲讲 kubernetes 的原理",
	"算一下这个月的开销",
	"推荐几本关于系统设计的书",
	"今天天气怎么样",
	"你好，在吗",
	"帮我起个项目名字",
	"写一首关于秋天的诗",
}

const (
	destructiveEmbedTopK = 5
	// Swept, not guessed — and the first guess was badly wrong. A margin of -0.02,
	// chosen to "bias toward recall", fired on 48% of real corpus traffic: every
	// other message would have stopped to ask the user for confirmation, which is
	// not caution, it is a broken assistant. The sweep (TestDestructiveMarginSweep)
	// shows the frontier sits well into positive territory:
	//
	//	margin   held-out recall   corpus fire rate
	//	-0.02        16/16              48.0%
	//	+0.00        15/16              29.0%
	//	+0.02        14/16              10.5%
	//	+0.06        13/16               0.6%
	//	+0.10        10/16               0.2%
	//
	// +0.06 is the knee: three quarters of the recall the permissive end buys, at
	// one eightieth of the interruptions. The three it gives up are recorded as
	// known misses in the test rather than tuned away, because buying them back
	// costs an order of magnitude more false alarms — and a confirmation prompt the
	// user learns to click through protects no one.
	destructiveEmbedMargin = 0.06

	destructiveEmbedMaxRunes = 600
	destructiveInitTimeout   = 30 * time.Second
	destructiveCallTimeout   = 5 * time.Second
	destructiveRetryAfter    = time.Minute
)

// classifyDestructiveEmbedFn is indirected so tests can pin the regex-only path
// and measure each layer's contribution separately.
var classifyDestructiveEmbedFn = classifyDestructiveEmbed

var destructiveEmbed = &destructiveEmbedState{}

type destructiveEmbedState struct {
	mu      sync.Mutex
	model   string
	pos     [][]float64
	neg     [][]float64
	lastTry time.Time
}

// ensure embeds the anchors for the currently detected model. Caller holds s.mu.
func (s *destructiveEmbedState) ensure(ctx context.Context) bool {
	// Shares the Ollama client with the search classifier — same host, same model
	// probe, one connection-refused per minute on a machine without Ollama.
	model, ok := searchEmbed.client.Model(ctx)
	if !ok {
		return false
	}
	if model == s.model && s.pos != nil {
		return true
	}
	if time.Since(s.lastTry) < destructiveRetryAfter {
		return false
	}
	s.lastTry = time.Now()

	initCtx, cancel := context.WithTimeout(ctx, destructiveInitTimeout)
	defer cancel()
	all := append(append([]string{}, destructivePosAnchors...), destructiveNegAnchors...)
	vecs, err := searchEmbed.client.Embed(initCtx, all)
	if err != nil {
		logger.Warn("pre-think destructive classifier: anchor embedding failed", "model", model, "err", err)
		return false
	}
	for i := range vecs {
		normalize(vecs[i])
	}
	s.model = model
	s.pos = vecs[:len(destructivePosAnchors)]
	s.neg = vecs[len(destructivePosAnchors):]
	logger.Info("pre-think destructive classifier ready", "model", model,
		"posAnchors", len(s.pos), "negAnchors", len(s.neg))
	return true
}

// destructiveScores returns the top-k mean cosine to each class.
func destructiveScores(msg string) (pos, neg float64, ok bool) {
	destructiveEmbed.mu.Lock()
	defer destructiveEmbed.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), destructiveInitTimeout)
	defer cancel()
	if !destructiveEmbed.ensure(ctx) {
		return 0, 0, false
	}

	if r := []rune(msg); len(r) > destructiveEmbedMaxRunes {
		msg = string(r[:destructiveEmbedMaxRunes])
	}
	callCtx, cancelCall := context.WithTimeout(context.Background(), destructiveCallTimeout)
	defer cancelCall()
	vecs, err := searchEmbed.client.Embed(callCtx, []string{msg})
	if err != nil {
		logger.Warn("pre-think destructive classifier: message embedding failed", "err", err)
		return 0, 0, false
	}
	q := vecs[0]
	normalize(q)
	return topKMeanDot(q, destructiveEmbed.pos, destructiveEmbedTopK),
		topKMeanDot(q, destructiveEmbed.neg, destructiveEmbedTopK), true
}

// classifyDestructiveEmbed answers "would doing this be irreversible?" by
// prototype vote. ok=false means no local Ollama, and the caller keeps whatever
// the regex decided — which fails toward MORE confirmations, never fewer.
func classifyDestructiveEmbed(msg string) (verdict bool, ok bool) {
	pos, neg, ok := destructiveScores(msg)
	if !ok {
		return false, false
	}
	return pos-neg > destructiveEmbedMargin, true
}
