package thread

import (
	"context"
	"testing"
)

// The regex layer alone, embedding pinned off. These are the phrasings the verb
// table is expected to know; the held-out test below is where the open half of
// the problem lives.
func TestNeedsCoderRegex(t *testing.T) {
	positives := []string{
		"帮我写一个 Python 脚本统计日志里的错误",
		"写个函数判断回文",
		"帮我做一个展示天气的网页",
		"写一个爬虫抓豆瓣评分",
		"帮我写个正则匹配邮箱",
		"write a script to resize all images in a folder",
		"implement a queue with two stacks",
		"build a website for my coffee notes",
		"refactor this into smaller functions",
		"把这段同步代码改成异步的",
		"帮我重构这个模块",
	}
	negatives := []string{
		"什么是依赖注入",
		"explain how garbage collection works in Go",
		"我该学 Rust 还是 Go",
		"Python 最新版本号是多少",
		"写一篇关于环保的演讲稿",
		"写一首关于秋天的诗",
		"写一个关于爬虫的故事",
		"帮我写一份产品需求文档",
		"总结一下这篇文章",
		"你好，在吗",
		"kill the process listening on port 3000",
		"今天天气怎么样",
	}

	for _, msg := range positives {
		if !needsCoderRegex(msg) {
			t.Errorf("regex missed a code production request: %q", msg)
		}
	}
	for _, msg := range negatives {
		if needsCoderRegex(msg) {
			t.Errorf("regex false positive: %q", msg)
		}
	}
}

// Pasted code plus a repair ask is decided deterministically, before the
// embedding layer: the paste truncates poorly and the answer is already known.
func TestNeedsCoder_PasteWithFixIntent(t *testing.T) {
	paste := "这段跑不起来，帮我修一下\n```python\ndef f(x):\n    return f(x)\n```"
	if !needsCoderRegex(paste) {
		t.Error("pasted code with a repair ask must fire")
	}
	explain := "解释一下这段代码在做什么\n```python\ndef f(x):\n    return x\n```"
	if needsCoderRegex(explain) {
		t.Error("pasted code with an explanation ask must not fire")
	}
}

// Held-out phrasings, DISJOINT from both the anchors and the regex table above.
// This is the set the embedding layer is paid to win: production requests worded
// outside the verb table, and explanation/search/ops lookalikes that share its
// vocabulary. Skips without a configured embedding backend.
func TestNeedsCoder_HeldOut(t *testing.T) {
	ctx := context.Background()
	if _, ok := classifyCoderEmbed(ctx, "probe"); !ok {
		t.Skip("no embedding backend configured")
	}

	positives := []string{
		"帮我搞一个自动签到的脚本",
		"能不能给我撸一个查天气的小工具",
		"用 JS 写个倒计时组件放到页面上",
		"whip up a bash one-liner to rotate these logs",
		"make me a portfolio site with a dark theme",
		"code me a telegram bot that forwards messages",
		"把这个递归改成迭代实现",
		"给这个项目加上单元测试",
	}
	negatives := []string{
		"递归和迭代哪个效率高",
		"what is the time complexity of quicksort",
		"帮我看看这段代码写得怎么样",
		"现在最流行的前端框架是什么",
		"你会写代码吗",
		"写一份这周的工作周报",
		"推荐一本学算法的书",
		"帮我把这个容器重启一下",
	}

	missP, missN := 0, 0
	for _, msg := range positives {
		verdict, ok := classifyCoderEmbed(ctx, msg)
		if !ok {
			t.Fatalf("classifier became unavailable mid-test on %q", msg)
		}
		if !verdict {
			missP++
			t.Logf("held-out miss (should be true): %q", msg)
		}
	}
	for _, msg := range negatives {
		verdict, ok := classifyCoderEmbed(ctx, msg)
		if !ok {
			t.Fatalf("classifier became unavailable mid-test on %q", msg)
		}
		if verdict {
			missN++
			t.Logf("held-out false positive: %q", msg)
		}
	}

	// Precision is the guarded axis: a false positive dispatches a subagent on
	// the most expensive routed model, a miss just leaves the code inline.
	if missN > 1 {
		t.Errorf("held-out false positives: %d/%d — the margin is too permissive", missN, len(negatives))
	}
	if missP > 2 {
		t.Errorf("held-out misses: %d/%d — the margin is too strict or the anchors too narrow", missP, len(positives))
	}
}

// TestCoderMarginSweep prints the pos-neg delta for every held-out case so the
// margin is chosen from data rather than guessed — the destructive classifier's
// first guessed margin was off by a factor that made it fire on half of all
// traffic, and this classifier gets the same treatment in the other direction.
func TestCoderMarginSweep(t *testing.T) {
	ctx := context.Background()
	if _, ok := classifyCoderEmbed(ctx, "probe"); !ok {
		t.Skip("no embedding backend configured")
	}

	cases := []struct {
		msg  string
		want bool
	}{
		{"帮我搞一个自动签到的脚本", true},
		{"能不能给我撸一个查天气的小工具", true},
		{"用 JS 写个倒计时组件放到页面上", true},
		{"whip up a bash one-liner to rotate these logs", true},
		{"make me a portfolio site with a dark theme", true},
		{"code me a telegram bot that forwards messages", true},
		{"把这个递归改成迭代实现", true},
		{"给这个项目加上单元测试", true},
		{"递归和迭代哪个效率高", false},
		{"what is the time complexity of quicksort", false},
		{"帮我看看这段代码写得怎么样", false},
		{"现在最流行的前端框架是什么", false},
		{"你会写代码吗", false},
		{"写一份这周的工作周报", false},
		{"推荐一本学算法的书", false},
		{"帮我把这个容器重启一下", false},
	}
	for _, c := range cases {
		pos, neg, ok := coderScores(ctx, c.msg)
		if !ok {
			t.Fatalf("classifier unavailable on %q", c.msg)
		}
		t.Logf("delta=%+.4f want=%-5v %q", pos-neg, c.want, c.msg)
	}
}
