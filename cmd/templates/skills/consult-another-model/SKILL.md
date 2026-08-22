---
name: consult-another-model
description: Use when you find a task difficult — complex reasoning, world knowledge, or details you do not actually know; two consecutive failures to answer the user correctly; an answer likely to leave the user disappointed; a question that must be answered correctly the first time; or any moment where you notice you are guessing rather than knowing. Escalates one bounded question through dispatch to a model of a different architecture or price tier, then applies the answer yourself. Also use when the user explicitly says "think about it more carefully", expresses disappointment with your last answer, or asks you to think deeply about something.
---

# Consulting Another Model

Your turn runs on whichever model this session is routed to. That model was
chosen for cost and conversational fit — not for factual recall, not for peak
reasoning, and not for the specific thing the user just asked. When a question
sits outside what your model is good at, you do not have to guess. You can put
one bounded question to a **differently built** model, get an answer back, and
act on it.

This is not always "escalate to something smarter". Models differ by
architecture and training data as much as by tier, and the right consultant
depends on what kind of hard the question is. A model that beats yours by 14
points on reasoning may be *worse* than yours at remembering a fact.

The mechanism is a **subagent dispatch carrying a per-wake model override**. It
is the one place a model is selectable from a conversation: you may name a
model for a child you spawn, never for yourself.

```
dispatch(sends=[{
  to: "subagent",
  params: {
    agent: "general",
    task_id: "consult-visa-rules",
    provider: "openai-oauth",
    model: "gpt-5.6-sol[xhigh]"
  },
  body: "<the complete, self-contained question — see 'Writing the question'>"
}])
```

`provider` and `model` must be set together. They are validated at dispatch
time against this deployment's configured keys and that provider's whitelist,
so a wrong pair fails the whole batch with an error naming what is usable — a
bad guess costs one retry, never a silent mis-route.

## The single most important number

Artificial Analysis publishes an **Omniscience** score: how much a model
actually knows, *netted against how often it states falsehoods with
confidence*. A negative score means the model's confident-wrong answers cost
more than its correct ones earn.

| model | Omniscience |
|---|---:|
| Gemini 3.5 Flash | **+22.7** |
| GPT-5.6 Sol (xhigh) | +20.6 |
| Kimi K3 | +18.4 |
| Claude Sonnet 4.6 (max) | +12.4 |
| Gemini 3.5 Flash-Lite | +6.9 |
| GLM-5.2 | +4.0 |
| MiniMax-M3 | +1.4 |
| GPT-5.6 Terra (xhigh) | **−3.3** |
| DeepSeek V4 Pro (max) | **−10.0** |
| GPT-5.6 Luna (max) | **−11.2** |
| DeepSeek V4 Flash (max) | **−22.9** |

That table is the published measurement, not a menu. Two of its rows name
models this deployment cannot reach: **Gemini 3.5 Flash** was retired from the
whitelist, and **every Claude model was removed** — the Anthropic provider is
gone and so are the `anthropic/*` routes on OpenRouter, on cost grounds. Do not
try to consult either; the dispatch fails validation. The reachable leader is
GPT-5.6 Sol (xhigh). Gemini is still reachable, but **only through
`openrouter`** — the native `gemini` provider was removed, so the id you dispatch
is `google/gemini-3.7-flash` (not in this snapshot; treat it as unmeasured, not
as a drop-in for the +22.7 row) or `google/gemini-3.5-flash-lite`, which is the
+6.9 row: a cheap second opinion, not a knowledge escalation.

Read the bottom of that table carefully: several models commonly used as
session defaults are **net-negative on world knowledge**. If you are running on
one of them and the user asks a factual question you are not certain about,
your most likely failure is not "I don't know" — it is a fluent, wrong,
confident answer. That is the exact situation this skill exists for, and the
swing from the bottom of the table to the highest row you can actually reach is
over 43 points.

Two models can reach a similar score by opposite routes. GPT-5.6 Sol attempts
nearly every question and is right about 55% of the time; Kimi K3 attempts far
fewer and is wrong far less often. When the cost of a confidently wrong answer
is high — the user will act on it, repeat it, or be embarrassed by it — prefer
the model that abstains.

## Who to consult, by what kind of hard

Snapshot of **2026-07-26**. `$/task` is AA's measured cost to complete one
benchmark task, which counts reasoning tokens actually emitted — the honest
number, not the per-token list price.

### Facts, world knowledge, things you do not know

| provider | model | Omniscience | $/task |
|---|---|---:|---:|
| `openai-oauth` | `gpt-5.6-sol[xhigh]` | +20.6 | 0.139 |
| `moonshot-cn` / `moonshot-global` | `kimi-k3` | +18.4 | 0.954 |

`gpt-5.6-sol[xhigh]` first — it leads on knowledge and costs 7x less per task
than `kimi-k3`. Choose `kimi-k3` when a confident wrong answer would be worse
than no answer.

**Do not consult GLM, MiniMax-M3, or MiMo for facts.** They score 4.0 (measured
on GLM-5.2), 1.4 and 3.6 despite respectable reasoning scores — strong at
thinking, weak at remembering.

### Complex reasoning, analysis, algorithms, code

| provider | model | Intelligence | Coding | $/task |
|---|---|---:|---:|---:|
| `openai-oauth` | `gpt-5.6-sol[xhigh]` | 57.7 | **78.3** | 0.139 |
| `moonshot-cn` / `moonshot-global` | `kimi-k3` | 57.1 | 76.2 | 0.954 |
| `openai-oauth` | `gpt-5.6-terra[xhigh]` | 51.6 | 70.6 | 0.055 |
| `zhipu-cn` | `glm-5.3` † | 51.1 | 68.8 | 0.319 |

`gpt-5.6-sol[xhigh]` first. `kimi-k3` when you want a genuinely different
architecture rather than a stronger one — useful for a second opinion that is
not just a louder version of the same reasoning.

† The GLM scores in this file were measured on **GLM-5.2**. The routable model
is now `glm-5.3`, which this snapshot does not cover — read those numbers as
its predecessor's, not as its own.

### Generating something — pages, layouts, charts, visual output

Ranked by Design Arena Elo, which is human head-to-head preference on design
and frontend generation, not a knowledge benchmark:

| provider | model | Design Elo | note |
|---|---|---:|---|
| `moonshot-cn` / `moonshot-global` | `kimi-k3` | **1420** | #1 overall, ahead of every OpenAI model |
| `zhipu-cn` | `glm-5.3` | 1347 † | measured on GLM-5.2 |
| `mimo` | `mimo-v2.5-pro` | 1313 | cheapest of the three by a wide margin |

This is the one category where the reasoning leaders lose: `gpt-5.6-sol` sits
at 1358, behind Kimi K3.

### A cheap different angle

When you want a second perspective more than you want raw capability:

| provider | model | $/task | why |
|---|---|---:|---|
| `openai-oauth` | `gpt-5.6-terra[xhigh]` | 0.055 | different tier, nearly free |
| `openrouter` | `google/gemini-3.1-flash-lite` | 0.034 | different vendor entirely |
| `minimax-cn` / `minimax-global` | `minimax-m3` | 0.125 | different architecture |

### Never consult these

- **Your own family's models, from your own session.** Consulting
  `deepseek-v4-pro` from a DeepSeek-routed session is a sideways move, not an
  escalation — same training data, same blind spots.
- **`-instant` aliases** (`deepseek-v4-flash-instant`). Thinking is disabled;
  they drop ~13 Intelligence points and exist for throughput.
- **Non-reasoning variants of reasoning models.** `gpt-5.6-sol` with reasoning
  off scores +0.85 Omniscience against +20.6 with `[xhigh]` — the same weights,
  a twentyfold difference in usable knowledge.

## Reading the model strings

- **A bracket suffix is an effort tier and is part of the model string.**
  `gpt-5.6-*` accepts `[low] [medium] [high] [xhigh]`; `deepseek-v4-*` accepts
  `[high] [max]`. No bracket means the provider's server-side default, which is
  not necessarily its best tier.
- **Higher effort is not monotonically better.** `gpt-5.6-sol[xhigh]` beats
  `[high]` on Coding *and* costs a quarter as much. Never assume "more effort =
  better answer, at a price".
- **Availability differs per deployment.** `nagobot set-model --list-fallback`
  shows which providers actually have keys and credit here. You do not need to
  check first — a rejected dispatch names the usable set in its error.
- **These numbers go stale.** Both sources are free and keyless. AA publishes
  every configuration inside the page payload of
  `https://artificialanalysis.ai/evaluations/artificial-analysis-intelligence-index`;
  Design Arena serves
  `POST https://www.designarena.ai/api/leaderboard {"arenaType":"models","category":"allcategories"}`.

## When to consult

Consult when **the cost of being wrong exceeds the cost of the consult**:

- You are about to state a fact you are not actually sure of, and the user will
  act on it.
- You have answered twice and the user is still not satisfied.
- The question needs specific knowledge — a date, a figure, a regulation, a
  version, a name — that you cannot verify from tools or memory.
- A genuine reasoning problem: a non-obvious algorithm, a subtle root cause, a
  design decision that is expensive to reverse.
- Something that must be right the first time because the user will forward it,
  publish it, or spend money on it.
- You catch yourself writing "should probably", "I think", or "typically" about
  something the user asked precisely.

Do **not** consult when:

- You already know the answer and want reassurance. That is a paid no-op.
- The gap is **retrievable**, not a matter of model capability — the fact is in
  a file, a session log, a memory file, or one `web_search` away. Search or
  read first. Another model does not have your user's data either.
- The question is about *this user* — their history, their preferences, their
  files, their past decisions. You hold that context; the consultant has none
  of it and will invent something plausible.
- The task is retrieval, formatting, summarizing, or tool orchestration. Those
  do not improve with a different model.
- You are inside a `heartbeat` or `compression` turn. Nothing you produce
  reaches anyone, so the spend is pure waste.

## Writing the question

**The subagent starts with zero context.** It cannot see this conversation, the
files you read, the errors you hit, or who the user is. A question that reads
naturally to you — "so why is this wrong?" — arrives as nonsense. This is the
single most common way a consult is wasted: a generic answer comes back and the
cost was paid for nothing.

A consult body must stand alone. Include:

1. **The concrete question**, in one or two sentences.
2. **The actual material** — the real text, the real code, the real error, the
   real numbers. Paste it; do not describe it, and do not send a file path and
   assume the consultant shares your working directory.
3. **What you already tried or already believe**, and why you are unsure. This
   is what stops the consultant from handing back your own dead end.
4. **The constraints** — language, versions, region, time period, what may not
   change.
5. **The shape of answer you need** — "give me the corrected function", "which
   of these two, and why", "just the number and your confidence in it".
   Without this you get an essay where you needed a decision.

For factual questions, **ask for the uncertainty explicitly**: "if you are not
sure, say so rather than guessing." The models worth consulting on facts will
comply, and an honest "unknown" is a usable answer — it tells you to search
instead.

Keep it to one question. A consult asking five things returns five shallow
answers; genuinely independent questions belong in parallel subagents with
distinct `task_id`s.

## Handling the answer

The consult is **asynchronous**. Your dispatch ends the turn. Later you wake
with `source: child_completed` and the consultant's answer as the wake body.

- **You remain responsible for the result.** The consultant saw none of the
  real context, so check its answer against reality before acting — read the
  file, run the build, run the search. An answer that assumes something which
  does not exist here is wrong regardless of which model produced it.
- **Apply it yourself.** Do not forward a wall of consultant prose. The user
  asked you. Extract the decision or the fact, use it, and answer in your own
  voice.
- **Mention that you checked, briefly**, when it materially changed your
  answer. One clause is enough; a paragraph of attribution is not.
- **A wrong or generic answer usually means your question lacked context**, not
  that the model was inadequate. Re-send once with the missing material,
  reusing the same `task_id` to continue that child. If it fails again, search,
  solve it yourself, or tell the user you are not sure — a third attempt will
  not succeed.
- **Do not chain consults.** Feeding one consultant's answer into another
  compounds the context loss at every hop.

## Telling the user you are working

A consult takes as long as a full turn on a large model. If a human is waiting,
batch the dispatch with your reply text so they are not left in silence — a
dispatch batched with other content delivers and the turn continues, whereas a
solo dispatch ends the turn:

```
Let me double-check this properly — one moment.
dispatch(sends=[{
  to: "subagent",
  params: {agent: "general", task_id: "consult-tax-rule", provider: "openai-oauth", model: "gpt-5.6-sol[xhigh]"},
  body: "..."
}])
```

## Examples

```
# A factual question you are not certain about — route to the knowledge leader,
# and ask for uncertainty rather than a confident guess
dispatch(sends=[{
  to: "subagent",
  params: {agent: "general", task_id: "consult-visa", provider: "openai-oauth", model: "gpt-5.6-sol[xhigh]"},
  body: "Question: as of mid-2026, does a Chinese passport holder need a visa in advance for a 7-day tourist trip to Georgia (the country), and what is the maximum visa-free stay if not?\n\nWhat I believe but am not sure of: visa-free for one year.\n\nIf the rule changed recently, or you are not confident, say so explicitly instead of guessing. I need the answer plus how sure you are."
}])

# User is unhappy after two attempts — different architecture, full history included
dispatch(sends=[{
  to: "subagent",
  params: {agent: "general", task_id: "consult-retry", provider: "moonshot-cn", model: "kimi-k3"},
  body: "A user asked: <verbatim question>.\n\nI answered twice and both were rejected.\nAttempt 1: <paste> — they said it missed the point about X.\nAttempt 2: <paste> — they said it was too generic.\n\nContext they gave: <paste everything relevant>.\n\nWhat I need: what is the question actually asking that I keep missing, and a concrete answer to it."
}])

# Generating a page — Design Arena leader, not the reasoning leader
dispatch(sends=[{
  to: "subagent",
  params: {agent: "general", task_id: "consult-dashboard", provider: "moonshot-cn", model: "kimi-k3"},
  body: "Produce one self-contained HTML dashboard for <data pasted in full>. Dark mode, no external assets, must render standalone in a browser. Return the complete file and nothing else."
}])

# Two independent questions — parallel, distinct task_ids
dispatch(sends=[
  {to: "subagent", params: {agent: "general", task_id: "consult-a", provider: "openai-oauth", model: "gpt-5.6-sol[xhigh]"},
   body: "<full self-contained question A>"},
  {to: "subagent", params: {agent: "general", task_id: "consult-b", provider: "moonshot-cn", model: "kimi-k3"},
   body: "<full self-contained question B>"}
])
```
