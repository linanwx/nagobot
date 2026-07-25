# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o nagobot .          # Build
go test ./...                   # Run all tests
nagobot update                  # Self-update from GitHub Releases
```

Single package test: `go test ./provider -v -run TestSanitize`

## Architecture

nagobot is a Go-based AI bot framework. Messages flow through four layers:

```
Channel (I/O) → Dispatcher (routing) → Thread (execution) → Provider (LLM)
```

### Channel → Dispatcher (`channel/` → `cmd/dispatcher.go`)

Channels are pure I/O (Telegram, Discord, Feishu, Web, CLI, Cron). Each produces `channel.Message` structs. The Dispatcher routes messages to threads by computing a `sessionKey` (e.g., `"telegram:123456"`) and wrapping the message into a `WakeMessage` with Source, Sink, AgentName, and Vars.

The `/init` command is intercepted in the Dispatcher and executed directly via `initCmd.ParseFlags()` + `RunE()` — it does NOT go through the thread/LLM pipeline.

### Thread Manager (`thread/manager.go`)

Schedules up to 16 concurrent threads. `Manager.Wake(sessionKey, msg)` creates a thread if needed and enqueues the message. The Run() loop picks runnable threads and calls `RunOnce()`. Idle threads are GC'd after 3 hours.

### Thread Execution (`thread/run.go`, `thread/wake.go`, `thread/runner.go`)

`RunOnce()` dequeues a WakeMessage, merges consecutive same-source messages, builds the prompt, and runs the agentic loop (LLM call → tool execution → repeat). The `Runner` handles the iteration loop with hooks for streaming, message injection, and halt conditions.

Key: `resolveProvider()` calls `ProviderFactory.Create()` each time (not cached) so config changes from `/init` take effect immediately.

### WakeMessage Format (`thread/wake.go`)

Wake payloads use YAML frontmatter + markdown body with per-source sender:
- User messages (telegram/discord/cli/etc): `sender: user`
- System messages (child_completed/cron/sleep/heartbeat/etc): `sender: system`

Action hints for system sources explicitly tell the AI to include content in its response.

Media attachments ride the frontmatter, not the body: `media` carries the channel's resource summary (`[Media: photo] image_path: …`) and `media_preview` the upfront image description / audio transcription, both flattened to one line (`oneLine`). The body stays pure user speech. `tryMerge` folds media fields from merged messages with `|` — a merged turn renders ONE header, so unfolded fields would vanish. Both fields survive Tier-1 wake trimming (`wakeTrimKeys` is a blocklist and deliberately excludes them — transcripts are content, paths feed later `read_file`).

### Pre-Think (`thread/prethink*.go`, `embedding/`)

Every user message is analyzed before the main model sees it, producing the **action hint** in the wake payload. This used to be a blocking LLM call (a `fast` model, 10s timeout, 10 XML fields) into a `{sessionKey}:prethink` sibling session. **It is now regex + a remote embedding API (no LLM call, no sibling session, no local model).** Warm cost is one embedding round-trip: ~170ms from the CN VPS, ~1.5s from the Mac (whose route to siliconflow.cn goes the long way).

Five of the ten fields survived; the other five were deleted:

| Field | How | Notes |
|---|---|---|
| `has_web_url` | regex | Strictly *more* accurate than the LLM, which missed real `https://` links |
| `is_include_investigator` | regex, mask-then-match | |
| `search` | regex gates → embedding prototype → regex fallback | |
| `destructive` | precision gates → regex ∪ embedding, **reads recent chat** | "执行吧" is only dangerous because of the turn above it |
| `skills` | embedding retrieval over skill descriptions | Cannot hallucinate a slug that doesn't exist |
| `coder` | regex ∪ embedding | **No LLM ancestor** — added 2026-07 to route code-production requests ("写个脚本/网页", "fix this bug") to the coder subagent. Precision-biased: a false positive dispatches the most expensive routed model, a miss just writes the code inline. Margin swept on a held-out set (`TestCoderMarginSweep`), knee at -0.025 on the 4B |
| ~~`tone`~~ | deleted | 83% constant, copied from USER.md |
| ~~`is_multi_step`~~ | deleted | The LLM's verdict was effectively `len(msg) > 160`; an embedding classifier scored *below* an always-false baseline |
| ~~`hallucination`~~ ~~`needs_verification`~~ ~~`confusing_terminology`~~ | deleted | By decision |

**The embedding backend is remote and resolved from config** (`thread/prethink_backend.go`), by fixed precedence: `siliconflowCN` → `siliconflowGlobal` → `openrouter` → off. Key presence IS the selection (env vars `SILICONFLOW_API_KEY` / `OPENROUTER_API_KEY` also count — the container path); there is no health probing or failover. The model is pinned to **Qwen3-Embedding-4B on every backend** — same weights on SiliconFlow and OpenRouter, so one calibration covers the whole chain. The pin is measured (2026-07 migration bench): SF 4B answers in ~170ms p50 from a CN host with zero >2s calls in 25 probes, while **SF 8B blows a 2s budget on 9/25 calls (p90 10.6s)** — the same tail that poisons OpenRouter's 8B route whenever it lands on the SiliconFlow endpoint. Bigger was not better; do not "upgrade" to 8B without re-measuring that tail. The old local-Ollama client (`embedding.NewLocal`) and its sidecar are gone — remote costs cents/month while the ~1GB resident Ollama dictated VPS sizing.

**A remote backend key is a hard requirement for `destructive`, not an optimization.** Without one that field falls back to a verb table that scores 0/15 on held-out phrasings — and its failure direction is "irreversible action proceeds unconfirmed". Every other field degrades gracefully; the daemon logs a Warn at startup when no backend is configured.

**Qwen3-Embedding queries carry an instruction prefix** (`qwen3Instructed`), and which SIDES get it is measured per classifier: destructive and search instruct both anchors and queries, coder instructs the query only (both-sides interleaved its boundary to a 0.0004 gap), skills is query-side-only by construction. The instruction is worth real points — on the destructive held-out set, raw-text 4B misses 2 with 13∕15 recall; instructed 4B misses 0 with 15∕15 at open margins. Margins were re-swept for the 4B (`destructive` +0.05 against hand + held-out + 400 real user messages from this deployment's own session logs at 1.8% fire rate; `coder` -0.025; `skills` none-margin unchanged at 0.05 after adding doc-shaped none anchors). The three old known git misses shrank to two, both label-arguable.

`preThinkAction` runs the four embedding-touching classifiers **concurrently** under a 3s budget (`preThinkBudget`). Serially their timeouts would sum to 20s — worse than the LLM they replace. The budget is 3s, not 2s, because it must cover the p90 of one remote round-trip from the WORST deployed host (the Mac's ~1.5s route), or the semantic layer silently degrades to regex exactly where it is needed. Regex-only verdicts are computed first and stand as the fallback if the budget blows, so a timeout degrades to a weaker answer, never to `false` on `destructive`. `WarmLocalPreThink()` builds the four anchor indexes at daemon start so the first message doesn't pay for them.

**The budget context bounds the per-message query, NOT the index build** — and this split is load-bearing in both directions. Queries take the caller's ctx (via `ctxMutex.lock` and `context.WithTimeout(ctx, …)`), so a blown budget cancels the in-flight HTTP call and releases the goroutine instead of leaving it parked on a classifier mutex for a turn that is already answered. Index builds (`*.ensure()`) deliberately use `context.Background()`: a cold build takes seconds over the network, so binding it to the budget would cancel it every time, and the failed build's 1-minute `lastTry` cooldown would then make it retry-and-die forever — the embedding layer would never come up at all.

**Anchor tuning is measured, never guessed.** `scratchpad/label_prethink.py` labels a corpus with the real pre-think prompt to get ground truth; `thread/prethink_labeled_corpus_test.go` scores each detector against it. **Agreement with the LLM is not correctness** — that file's header documents two cases where the detector is right and the LLM's label is wrong. Real-traffic fire rates can be re-measured without that corpus: sample user messages straight from `{workspace}/sessions/**/session.jsonl` (strip the wake frontmatter, filter to real user sources).

### Progress Reporting (`thread/progress_scanner.go`)

While a turn runs long, the person waiting gets an AI-written progress note about once a minute. The `ProgressScanner` goroutine (started in `cmd/serve.go`) sweeps `Manager.ListThreads()` every 30s; a thread is eligible after 60s elapsed with ≥1 tool call. For each eligible thread it snapshots the live `ExecMetrics` — the turn's origin request (wake body, frontmatter stripped, ≤2000 runes, captured in `run()`) plus the tool trace (args/results each ≤500 runes at record time, `toolTraceFieldRunes`; last 40 calls per report) — and wakes the **`progress-summary` sibling agent** (`{key}:progresssummary`: tools disabled, stateless, `specialty: [lowcost]`, same pattern as media previews) to turn it into a 1–3 sentence note. Delivery splits by who is waiting:

- **Main user-facing session** → the note goes straight out the thread's `defaultSink` to the channel user (plus a `chat.jsonl` append with origin `progress`), bypassing the busy thread. Gated on the turn's wake source being user-visible — heartbeat/cron/compression/cross-session turns on a user-facing key never message the user.
- **Subagent/fork child** → the note rides a `WakeProgress` wake to the user-facing ancestor, whose LLM decides between plain reply text (delivered to the user by `contentSink`) and `dispatch({})` (silent). Child turns report only for `session`/`resume` wake sources. These turns used to require an explicit `dispatch(to=user)` and carried a capped nudge loop (`progressMaxDispatchNudges`); both are gone.

The monitored thread is never touched. If the observed turn ends before the summary returns (checked via `Manager.runningTurnThread` — `ExecMetrics.TurnStart` identifies the turn), the note is dropped. Internal sibling sessions are excluded from monitoring (recursion guard). Without the `progress-summary` agent template the scanner does nothing — there is no mechanical fallback; the old scanner that pasted the raw tool trace to the ancestor was replaced by this system.

### Reply quotes (`thread/quote.go`, `channel/web_quote.go`)

The web client's quote-reply button asks the daemon to turn the message being replied to into **ONE complete line of markdown quote, leading `> ` marker included** — `POST /api/quote {session_id, text} → {quote}`. It is the fifth instance of the sibling-agent one-shot pattern: `Manager.Quote` wakes `{key}:quote` (`session.QuoteSessionSuffix`, in `IsInternalSiblingSession` so the progress scanner skips it) with `WakeQuote` on the `quote-summary` agent (tools disabled, stateless, `specialty: [fast]`) and blocks up to 20s on `OnComplete`.

It is the one sibling that routes through `fast` instead of `lowcost`, and the split is the point: `lowcost` optimizes price for background work nobody is waiting on, while a quote runs with a human watching a spinner. `fast` is meant for a non-reasoning, high-throughput model — on every deployment today `deepseek/deepseek-v4-flash-instant`, whose `-instant` alias disables thinking. Measured on kingsley, the same quote request went from **4354ms with 298 reasoning tokens** (`lowcost` → `deepseek-v4-flash`, thinking on) to **1929ms with 0** — a task that needs no reasoning was spending a reasoning budget on it. `fast` is deliberately neither restricted nor optional in `provider/specialty.go`: an absent rule cascades to the default model, so a deployment that never configured it gets a slower quote, never a broken button.

The whole format is the model's output, produced from the quoted message alone. That is the design constraint, not an implementation detail: a table, a code block, long prose and a one-liner all take the same path and come back as a short human-readable reference, so there is **no structural parsing of the source text anywhere** — no per-block rules, no truncation heuristics, no `> `-prefixing of the original. Post-processing is exactly a whitespace collapse (`oneLine`, the result must be one line), a fail-loud check that the line starts with `>`, and a 200-rune backstop. A missing marker is an error, never repaired: prefixing it would hide a broken template or a mis-routed model.

**There is no fallback, at any layer.** Missing agent template → `ErrQuoteUnavailable`; timeout / bad format → error; the handler returns 502 (503 only when no generator is injected at all) and the button shows an alert icon with the reason in its tooltip. A mechanically mangled quote is worse than a visible failure.

Two seams keep the LLM swappable. Server-side: the channel holds an opaque `func(ctx, sessionKey, text) (string, error)` injected by `WebChannel.SetQuoteFn` (wired in `cmd/serve.go`, same pattern as `SetSystemPromptFn`), so replacing the generator is one line there and `channel/` never learns what quote syntax is. Client-side: `chat-pane.tsx` is the only file that knows both the session key and the endpoint; `quote-reply.tsx` (`QuoteReplyProvider` / `ReplyQuoteButton` / `ComposerQuoteBar`) takes a plain `(text) => Promise<string>` and renders nothing without a provider.

**Nothing on the display side is quote-aware.** assistant-ui carries the pending quote as `AppendMessage.metadata.custom.quote` and does *not* prepend it, so `use-nagobot-chat.ts:onNew` does — from that point the quote is just the first line of an ordinary markdown message. That is why persistence and reload came for free once user bubbles got markdown (`UserMarkdownText` + `userMarkdownComponents` in `thread.tsx`): the leading `> …` line renders as a real `<blockquote>` in live and reloaded history alike. Those bubble components derive every color from `currentColor` (`bg-current/15`, `border-current/40`) because one map has to work on both `bg-primary text-primary-foreground` (the viewer's own messages) and `bg-muted text-foreground` (everyone else's) — the registry `MarkdownText`'s `aui-md` palette assumes a page background and renders `code` as a black block with invisible text on the dark bubble.

Quotes are deliberately **not linked to a message id**. `QuoteInfo.messageId` is a required assistant-ui type field that nothing reads; there is no jump-to-message, and a quote is a piece of text, not a reference.

### Pins (`thread/pin.go`, `channel/web_pins.go`, `client/src/components/pins-dialog.tsx`)

A pin is one markdown file in `{sessionDir}/pins`, and the collection is the session's curated record — the answer to "that thing we settled three weeks ago" without re-reading the log. The web client contributes a **📌 button beside the quote button** on every assistant message and a **Pins panel** behind the header's ⋯ menu.

The load-bearing asymmetry is **write goes through an LLM, read and delete do not**. Deciding whether new material belongs in an existing note is a judgement call; listing files and unlinking one is not. So `POST /api/pin` is the sixth instance of the sibling-agent pattern, while `GET/DELETE /api/pins` never touch a model.

- **Writing**: `Manager.Pin(parentKey, text)` wakes `{key}:pin` (`session.PinSessionSuffix`, in `IsInternalSiblingSession`) with `WakePin` on the **`pin-writer`** agent. Unlike every other sibling this one keeps its tools — grepping the existing pins, reading one, and writing the file IS the job — and routes through `toolcall` for the same reason.
- **It returns as soon as the wake is enqueued, and the endpoint answers 202, not 200.** The file does not exist yet when the client hears back. Everything downstream follows from that: the button's green ✓ acknowledges the *request*, and the panel polls every 5s because a user who pins and immediately opens it would otherwise stare at a stale list with no signal that more is coming. The 2.5s ack window doubles as the anti-spam guard — a user who sees no file cannot queue the same message five times.
- **Concurrency is the sibling's inbox, and that is why a sibling exists at all.** Every pin for a session lands in one thread's queue, so two pins can never race to merge into the same file. Running this as an agent override on the user's own thread would both pollute the conversation and fight the user for the queue.
- **Frontmatter is the dedup index**: `title` + `summary`, one line each. The agent's first act is one `grep "^title:|^summary:"` over `pins/*.md` — every existing subject in a single call — and it merges only when the subject already exists, creating a new file when it is a toss-up (two notes merge easily later; one wrongly-merged note has lost its structure). Merging must **keep every fact already filed** and fold the new material in rather than appending a second copy.
- **A pin with no parsable title falls back to its file name** (`pinEntryFrom`). A hand-written or half-written file must still be a clickable row, never a blank one. The frontmatter parser is deliberately minimal — two known scalar keys, malformed input degrades to "no fields" → filename fallback, not an error.
- **`name` is validated by SHAPE, not by resolve-then-contain**: a bare basename ending in `.md`, no separators, no `..`. The only legal name has no path structure at all, so shape validation is sufficient and the rejection stays legible (400). Deleting an already-gone pin is **204, not 404** — a double-click is not a failure.
- Client-side, `pin-message.tsx` takes a plain `(text) => Promise<void>` from `PinProvider` and never learns what a pin file looks like — the same seam quote-reply keeps. `chat-pane.tsx` is again the only file that knows both the session key and the endpoint.
- The panel renders content with its **own** `pinMarkdownComponents` map. The registry `MarkdownText` renders through the assistant message-part context, which a file read off disk does not have — the same reason event cards and user bubbles have their own maps. Frontmatter is stripped before rendering; the row header already shows both fields it carries.

### Inline images on web (`channel/web_media.go`, `client/src/components/assistant-ui/markdown-image.tsx`)

The `send-image` skill has always been "write `![alt](path)` in your reply"; `SendResponse` then re-parses the **completed** text and hands each ref to the channel's optional `ImageSender` (`channel/image_sender.go`), which Discord and WeCom implement by uploading a native attachment. Web never implemented it — the skill's table said `not yet` — so the markdown reached the browser as a bare `<img>` pointing at a filesystem path, which resolved against the site origin and 404'd. Two shapes appeared in the wild for the same file: `/root/.nagobot/workspace/media/…` and `media/…`.

Web deliberately does **not** implement `ImageSender`. An attachment always lands at the end of the message, but the skill lets a reference sit anywhere, including mid-sentence — web renders markdown natively, so it is the only channel that can honour that placement, and an attachment would have been a downgrade *plus* left the inline broken image in place.

The load-bearing decision is **where the path is resolved**: entirely on the server, never in the client.

- **The client does no path interpretation at all.** `mediaSrc` passes through `http(s):`/`data:`/`blob:`/`/api/media…` and otherwise emits `/api/media?path=<encodeURIComponent(raw)>` — the model's string, verbatim. The rejected alternative was finding the `/media/` segment client-side and taking the tail, which **fails open**: `/home/me/photos/media/cover.jpg`, a path outside the workspace entirely, would map onto `{workspace}/media/cover.jpg` and silently render an unrelated image. Wrong picture beats broken picture only in the sense that it is harder to notice. `TestResolveMediaPath_RejectsEscapes` pins that exact decoy.
- **`resolveMediaPath` is the single authority**, and its resolve step mirrors `tools.resolveToolPath` (absolute stays, relative joins the workspace) so the set of paths the model can `read_file` and the set the browser can display are the same set by construction. Then containment under `{workspace}/media` is the whole security boundary — which is why **path shape is not a security variable** and both forms stay legal. Symlinks are evaluated on **both** sides before comparing: evaluating only the candidate breaks wherever the workspace itself sits behind a link (macOS `/tmp` → `/private/tmp`, i.e. every test run), evaluating neither lets a link planted in `media/` point anywhere on disk.
- Two URL forms share that one resolver: `/api/media/<sub>` (relative to `{workspace}/media`; now allows subdirectories, which is what made `media/bilibili-cover/…` reachable — the old `filepath.Base` flattened it to a nonexistent sibling) and `GET /api/media?path=…`. The query form exists because an absolute path cannot ride in a URL path segment — `/api/media//root/…` has its double slash normalized away by the mux. The upload round-trip is untouched: `handleMediaUpload` still returns a basename, which is still a valid subpath.
- A refusal is **403, not 404** — the path was understood and rejected — and the client turns it into a visible "image unavailable" chip carrying the alt text. A convention violation must be legible; a broken-image glyph reads as "the bot is buggy" when the fact is "that file is not somewhere the page can serve".
- Cache is `private, max-age=604800`. `private` because this sits behind `protected()` with Caddy in front. No `immutable`: most names carry a timestamp+hash, but some (a per-video cover) are legitimately re-downloaded under the same name, and `immutable` would pin stale bytes for the full week. `http.ServeFile` still emits Last-Modified/ETag, so post-expiry is a 304.

**There is no streaming guard, and that is measured, not assumed.** The intuition that a half-streamed `![alt](media/bili` fires a burst of doomed requests is wrong: CommonMark needs the closing `)` before an image node exists, so parsing every prefix of a streaming message yields exactly **one** distinct image URL — the complete one. Don't add a guard for it without re-deriving that.

`img` is wired into all three renderer maps — `defaultComponents` (assistant), `userMarkdownComponents` and `eventMarkdownComponents` (both in `thread.tsx`). Missing any one of them leaves a class of message rendering raw broken images.

### Republishing a hosted page (`cmd/upload_html.go`, `create-html` skill)

`upload-html` puts a self-contained HTML file in R2 under `pages/<timestamp>-<rand>.html` and returns the public `pub-*.r2.dev` URL. Until 2026-07-25 that was the only thing it could do, so a revision meant a *second* page at a *second* URL — visible in the live workspace as `chester-sunday-map-20260621{,-v2,-v3}.html` and four overlapping cornwall guides among 112 files. `--replace <url|key>` now republishes over an existing key, keeping the URL.

**R2 was never the obstacle; `Cache-Control` was.** PutObject on an existing key is an atomic replace (new ETag, no DELETE, no versioning) and the r2.dev edge does not cache at all — responses carry no `cf-cache-status`, so an overwrite is visible to a fresh client instantly. What broke was the **browser**: the old header was `public, max-age=31536000, immutable`, and measured in a real browser, after overwriting, a plain navigation back to the same URL still rendered the pre-overwrite page. Only a forced `location.reload()` saw the new one — and a user clicking the link you sent them does not hard-reload. Hence `r2CacheControl = "public, max-age=0, must-revalidate"`: the ETag makes an unchanged page a 304 with a zero-byte body (verified against r2.dev), so revalidating every navigation costs one conditional request and a changed page is never missed.

**There is no rescue path if the header is wrong**, which is why it is a constant and not a flag: Cloudflare's Cache Purge API only covers zones, and `pub-*.r2.dev` is a managed domain with no zone. Pages published *before* this change carry a one-year immutable entry in every browser that opened them and can never be updated in place; that backlog was deliberately written off rather than special-cased.

`--replace` takes the URL a previous upload returned (what the model actually has on hand) or the bare key, and `replaceTargetKey` rejects rather than guesses — foreign origin, another prefix, `..`, or a bare prefix all fail, since the value is model-supplied and a wrong key overwrites an unrelated page. A `HeadObject` existence check comes before the PUT: without it one garbled character silently publishes a phantom page and the command reports success. There is deliberately **no local registry and no page-id system** — the URL is the id, it is already in the transcript and already in the user's chat. The cost is bounded and accepted: once compression ages the URL out of context, that page can no longer be updated, and the model is told to publish a new one rather than guess a key.

### Agent Templates (`agent/`)

Agents are markdown templates in `{workspace}/agents/{name}.md` with `{{PLACEHOLDER}}` syntax. Variables set via `agent.Set(key, value)` before `Build()`. Runtime vars (TOOLS, SKILLS, USER) are set per-turn in `thread/run.go`. `{{DATE}}` and `{{CALENDAR}}` are auto-resolved in `agent.Build()` at day-level granularity (no minutes/seconds).

**Important**: `{{WORKSPACE}}` is resolved in both `agent.Build()` and `use_skill` (`tools/skills.go`). Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.

### Model Resolution (`config/models.go`, `thread/run.go:resolvedModelConfig`)

Each turn resolves its provider+model from `thread.models` in config.yaml — a typed **list** of `ModelRule{Type, Name, Provider, ModelType}` where `Type` is `session | agent | specialty`. Precedence is by type, highest first:

1. **session** — rule whose `Name` == the turn's session key
2. **source-specialty** — if the turn's wake source matches a key in the agent's `source_specialty` frontmatter map, its specialty list is tried left-to-right (against the same `type:specialty` rule pool); applies only when `t.lastWakeSource` matches
3. **agent** — rule whose `Name` == the active agent name
4. **specialty** — the agent's `specialty` array, tried left-to-right; first specialty with a matching rule wins
5. **default** — top-level `thread.provider`/`thread.modelType`

Every step **cascades**: a declared-but-unconfigured entry (e.g. `source_specialty: {heartbeat: [lowcost]}` with no `type:specialty name:lowcost` rule) falls through to the next step, not to the default. So removing the `lowcost` rule drops heartbeat to the agent rule, then basic specialties, then default — graceful degradation, not an error.

`config.FindModelRule(rules, type, name)` does the lookups (linear scan; the list is small). Returns nil → caller falls back to the default model via `DefaultModelFn`.

Agent `specialty` is an **array** (`specialty: [cron, toolcall]`); parsing is lenient (`agent.StringList` accepts a scalar `specialty: pdf` as `[pdf]`, so hand-edited agent files don't silently mis-route). The cron-runner agents (tidyup/world-knowledge/people-knowledge) carry a leading `cron` specialty so a `type:specialty name:cron` rule can route them.

Agent `source_specialty` is a **map** of wake source → specialty list (`source_specialty: {heartbeat: [lowcost]}`); parsed in `agent.AgentDef.SourceSpecialties`, applied in `resolvedModelConfig` via `t.lastWakeSource`. `soul` ships with `heartbeat: [lowcost]` so heartbeat turns (dream / session-reflect) can route to a value model (e.g. `lowcost` → openai-oauth/gpt-5.6-luna, the cheapest mainline Codex model — see the Codex rate card: Luna 25/2.5/150 credits per 1M input/cached/output vs gpt-5.5 at 125/12.5/750, same rate as Sol; nothing on the plan is free) without changing the user-facing model. Note: `session-stats`'s `resolveModelChain` has no live wake source, so it shows the **non-source** chain only — source-specialty routing is not reflected there.

CLI writers: `set-model --type X --provider P --model M` upserts a `specialty` rule; `set-agent --session S --provider P --model M` upserts a `session` rule (and `--agent A` independently writes meta.json — session→model and session→agent are separate). Bare `set-agent --session S` clears both. `cmd/session_stats.go:resolveModelChain` mirrors this resolution for `nagobot session-stats`.

**`set-model --list`'s agent table reads the workspace, not the embedded templates** (`scanAllAgents`), because that is where `agent.NewRegistry` reads at runtime — same two dirs, same order, `agents-builtin` overriding `agents`. It scanned `templateFS` until 2026-07-25, so any divergence between the binary's templates and the deployed workspace made the table assert a specialty the daemon was not using: observed live when a hand-edited `quote-summary` routed through `fast` in the request logs while the table still printed `lowcost`. Embedded templates remain only as a fallback for a workspace that has nothing (pre-`onboard`), and the table's header names whichever source it used. A routing inspector that can contradict the router is worse than none.

**Removed (hard switch, v1.5.63)**: the old `thread.models` *map* (specialty→model), the `fixed-to-*` generated agents, and the implicit `provider/model` / bare-model specialty routes. Per-session pins are now `type:session` rules, not `fixed-to-*` agents. Config is NOT auto-migrated — the old map format fails to load (fail-fast); converting config.yaml is a manual deploy step.

### Provider Layer (`provider/`)

Each provider implements `Provider.Chat(ctx, *Request) (ChatResult, error)`. `ChatResult` has a basic variant (`Wait()` only) and a streaming variant (`StreamChatResult` with `Recv()`, `Wait()`, `Cancel()`). Streaming providers emit `StreamDelta` values (text, tool-call-start) through a channel; the Runner pulls deltas via `Recv()` loop and independently decides whether to forward to sink or fire events. This decouples provider streaming from sink delivery — e.g. Gemini streams at the provider level but content is filtered before user delivery (thinking leak protection). Events (emoji reactions) work for all providers regardless of streaming mode.

The `ProviderFactory` creates providers on demand, re-reading config each call. Providers enforce model whitelists. `SanitizeMessages()` removes orphaned tool messages before API calls.

**Provider instances are turn-scoped; openai-oauth WS connections are session-scoped.** `resolveProvider()` builds a fresh provider every turn (that's what makes `/init` config changes take effect), but the Codex backend scopes `previous_response_id` to the WebSocket session that produced it — so tearing the WS down at end of turn forced every turn's first call to resend full context. Measured on same-gap calls (<1min since previous), that is a **55% vs 94% cache hit rate**. The fix mirrors official codex's `cached_websocket_session`: `provider.WSPool` (held by the Factory, injected via `wsPoolSetter`) parks connection + continuation state per session+model at `Close()`, and the next turn's `ensureWSConn` adopts both. Invariant: **connection and continuation state live and die together** — adopting one without the other is never correct.

Details that are load-bearing:
- **Delta gate = prefix DeepEqual + instructions + tools fingerprint** (`buildRequestBody`). In-turn only the prefix check bites; cross-turn, instructions change on `{{DATE}}` rollover / USER.md rewrites and tools change on skill reloads — sending `previous_response_id` with different attributes is undefined server behavior, so any mismatch drops to full context and invalidates (mirrors codex's exhaustively-destructured `responses_request_properties_match`).
- **Parked connections have NO reader.** gorilla/websocket read errors are *permanent* (even a deadline timeout poisons the read side — handing off via deadline pokes panics with "repeated read on failed websocket connection"), so the pool's keepalive is client `WriteControl` pings only. Consequence: a server-side drop while parked is invisible until adoption.
- **A stale adopted connection costs one redial, never the turn.** `wsFailed` is sticky per turn; without special-casing, a dead pooled socket would demote the whole turn to HTTP full-context (losing in-turn delta too — strictly worse than not pooling). So a pooled connection's write failure or pre-emission stream failure gets exactly one fresh-dial retry (`writeWSRequest` rebuilds the body — the delta referenced a continuation that died with the old socket); only the retry's failure sets `wsFailed`.
- **Error paths call `closeHard()`, never `Close()`.** `Close()` (the end-of-turn `provider.Closer` hook) parks healthy connections; error paths run before `wsFailed` is set, so calling `Close()` there would park a broken connection.
- Pool TTL is 15 min (past that Tier-1 compression has rewritten history and killed the delta anyway); capacity 16 (= thread manager cap), LRU-evicted. The cross-turn fidelity premise (JSONL round trip + echo synthesis are conversion-invariant) is pinned by `openai_continuation_roundtrip_test.go` and was verified against 5 real sessions (~5,400 items) before shipping.
- **The context budget trim is the delta's other natural enemy** (`thread/runner.go:trimMessageGroups`). When the estimated request exceeds `contextLoopBudget` it halves the conversation: drops the oldest whole turns (cut at user-message boundaries; system prompt and the final turn always kept), in drop amounts **quantized to multiples of budget/2**. The quantization is the point — any rule that trims to a moving target advances the cut a little every call as the tail grows, rewriting the request head and breaking the prefix DeepEqual each time (measured live: one Tier-2 turn made 4 calls × ~175K prompt tokens at ~0% cache hit, ~44 credits, because the head moved before every call). Quantized, the head moves once per ~budget/2 (~123K on terra) of growth. The trigger line is `min(92% × window, window − maxTokens − 10K)` (245,616 on terra with the 16,384 maxTokens default) — deliberately percentage-based like the tier reserves, and ABOVE both of them (Tier 2 at 70%, Tier 3 at 85%), so compression always gets the first chance and the trim is a true last resort. The old formula `(window − maxTokens) × 0.9` mixed a proportional slack with the tiers' capped-constant reserves, which made the lines cross on large windows (at 1M the trim fired 25K BEFORE Tier 2 — trimming before compression ever ran). If whole turns can't meet the quota (one mega-agentic turn), it falls back to dropping that turn's oldest tool groups under the same quota. Still-over-budget logs a `Warn`, never silent; a prefix break logs `openai continuation dropped: history prefix changed` with the first-diff index. Trimming is ephemeral — the session file is untouched, so Tier-2 eligibility still sees the full size.

### Tools (`tools/`)

Tools implement `Def() ToolDef` + `Run(ctx, args) string`. Registered in a `Registry`, cloned per-thread. Search and fetch tools use `SearchProvider`/`FetchProvider` interfaces with runtime `Available()` checks.

`dispatch` is the routing tool for reaching OTHER agents (4 targets: caller:session / subagent / fork / session). **There is no `to=user`** — speaking to your own human is not a dispatch, it is just the turn's plain content, routed by `Thread.contentSink` (see below). The caller:session form asserts the caller is another session — a mismatch fails validation so the LLM can't silently misroute. `to=session` has two mutually exclusive addressing forms: `session_key` (must exist on disk — typo protection) and `channel`+`user_id` (endpoint form; channel is enum-validated against `endpointChannels`, the derived `channel:user_id` session is created if missing — the deliberate first-contact path; body is a wake message for the target session's AI, never verbatim text to the human). `dispatch({})` with empty sends ends a turn silently. **Solo rule**: dispatch terminates the turn only when it is the sole tool call in the assistant message (runner passes the batch size via `provider.WithToolBatchSize`); batched with other tool calls it still delivers every send but the turn continues (outcome `delivered-turn-continues`, empty batched dispatch → `no-op`) — halting would discard the sibling tools' results. A batched caller:* delivery also calls `ClearSuppressSink` so the turn's eventual final text still reaches the sink. For delayed self-wakes (replacing the old `sleep_thread(duration=...)`), use the `manage-cron` skill to create a one-time `set-at --direct-wake` job into the current session.

### Content delivery is decided by the wake source, not the model (`Thread.contentSink`)

Plain assistant content is speech to the session's OWN human. `thread/dispatch.go:contentSink` picks its destination from the wake source and the session, and the model has no say:

| situation | content goes to |
|---|---|
| user-visible wake (the human just spoke) | the wake's own sink, untouched — it carries stream binding, chat.jsonl, reply threading |
| non-user-facing session (subagent / fork / cron's own session) | the wake's sink; the runner separately forces an explicit dispatch there |
| `heartbeat*` / `compression` on a user-facing session | **nowhere** — a zero sink |
| anything else on a user-facing session (`cron`, `session`, `progress`) | `defaultSink`, i.e. the channel user |

This replaced `dispatch(to=user)`. Consequences worth knowing:

- **A heartbeat/dream turn cannot reach the user however it phrases itself.** This no longer depends on the LLM remembering `dispatch({})`, nor on the scheduler happening to attach a drop sink — the check is explicit and prefix-matches legacy `heartbeat_reflect` / `heartbeat_wake` sources.
- **`to=session` into a user-facing session now speaks to that session's human**, which is what the deployed weekly-review cron (dispatcher → per-person wecom sessions) relies on. Replying to the *caller* still requires `dispatch(to=caller:session)`; `currentSink` is untouched, so `SendToCaller` is unaffected.
- **`RequiresExplicitDispatch` is no longer sufficient on its own**: `run.go`'s `OnNoToolCalls` enforces it only when `!t.IsUserFacing()`. A user-facing session woken by a peer can legitimately end with plain text (it reaches its human); a subagent cannot (it has no human), so it keeps iterating until it dispatches.
- **Proactive content is logged separately.** Delivery on `defaultSink` bypasses the per-wake chat.jsonl sink, so `recordProactiveChat` appends it, keeping pre-think's recent-chat context aware of bot-initiated messages. The `origin` field records the driving source (caller key, or e.g. `cron`).
- **An internal sibling session is NOT user-facing, even though its key starts with one that is.** `isUserFacingKey` excludes `session.IsInternalSiblingSession` suffixes (`:quote`, `:imagepreview`, `:audiopreview`, `:progresssummary`, `:prethink`) alongside the `:threads:` / `:fork:` infixes. Without that term, `web:abc:quote` matched the `web:` prefix and was treated as having a human, so the sibling's entire output — which is a value returned to the caller via `OnComplete`, not speech — was routed to `defaultSink` as proactive content. The drop sink these callers pass does not protect against this: `contentSink` ignores the wake sink on that branch. On web the damage was maximal, because no page is ever bound to a sibling key: `WebChannel.Send` found no client, no participant record, and fell through to a **broadcast push to every enrolled device** titled `nagobot · web:abc:quote` — plus a `recordProactiveChat` line polluting pre-think's recent-chat window. Any new sibling suffix must be added to `IsInternalSiblingSession`, which is now load-bearing twice: progress-scanner recursion guard AND delivery routing.
- **Content alongside a dispatch call is legitimate, and dispatch delivers it (`DispatchHost.SettleTurnContent`).** The old rule hard-rejected assistant content ≥50 runes emitted with the tool_call ("move it into a send body") — correct while `to=user` existed, unsatisfiable without it, since no send body reaches one's own human. It was deleted with the target. What replaced it: report-to-your-reader-in-content + route-with-dispatch is the intended shape, and dispatch settles the content once it knows how the turn ends.

  The gap it closes is a real one. `run.go`'s `OnMessage` delivers a tool_call-bearing assistant message **only on chunkable sinks**, because such a message is normally intermediate and a plain final message is expected to follow. A solo dispatch falsifies that — the turn halts, no final message comes — so on a non-chunkable destination the text fell between "not delivered now" and "no later chance". `SettleTurnContent(ctx, content, deliver)` runs at the halt: it no-ops when the sink is chunkable (the runner already sent it — the one guard against double delivery), sends on `deliver=true` (solo dispatch with sends), and otherwise `logger.Warn`s the drop and tells the model. It is called AFTER the sends execute, so an executed `to=caller:session` — which suppresses the sink, the same destination content would take on a subagent — wins that one destination; waking a caller twice is worse than dropping prose.

  `ContentReachesSomeone()` consequently **dropped its `Chunkable` term**: with the gap closed, a non-chunkable destination still reaches its reader, so the predicate is now just non-zero sink **and** not suppressed. It has exactly one remaining job — never demand text a turn cannot deliver. Note this is what made `web:` sessions stop taking a false reject path: their defaultSink is not chunkable, which used to read as "nobody is reading this session".

**A turn someone is waiting on may not end on a bare dispatch.** When the dispatch is solo (so it terminates the turn), `sends` is non-empty, and the assistant message carries no content, the call is rejected before execution — nothing is sent and the turn continues. Without this, a model that hands work to a subagent and stops leaves the person who asked staring at silence, since content is the only channel to one's own reader.

"Someone is waiting" is two wakes, not one:

- **the human just spoke** (`CallerKindUser`), or
- **our own subagent/fork just reported back** — `CallerKindSession` where `DispatchHost.CallerIsOwnChild()` holds. This is the return leg of work WE delegated: somewhere up the chain a human asked turn 1's question and is still waiting, so ending silently when the answer arrives strands them. `WakeSession`/`CallerKindSession` conflates this with "a peer asked me something" and the wake source cannot separate them; the **session key** can — children are created as `{parent}{ThreadsSessionInfix|ForkSessionInfix}{taskID}`, so a prefix test on `currentCallerKey` decides it.

A genuine peer wake stays exempt: nobody there is waiting on a human-facing reply, and narrating every peer exchange would be worse than silence.

Two more narrowings, both load-bearing: **solo only** (batched, the turn continues and the model can still speak); **`ContentReachesSomeone()` only** (demanding text a turn cannot deliver — heartbeat/compression — would be an unsatisfiable loop). Empty `dispatch({})` is exempt by construction: it returns from the highest-priority branch above, and it is the model explicitly choosing silence rather than forgetting to speak.

Field admission per target is a **whitelist** (`acceptedFields` in `tools/dispatch.go`), not a per-branch reject list: a new field on `DispatchSend` is rejected by every target until one opts in. This is why it was rewritten — the old hand-written reject lists checked agent/task_id/session_key/provider/model and forgot channel/user_id, which were then accepted-and-ignored on four of the six targets. `normalizeSends` trims every identifier field once at the entry point so presence checks, self-reference guards, existence lookups and execution can't disagree about whether `"cli:main "` is `"cli:main"`.

### Tool argument contract (`tools/tools.go`)

**Empty string IS "not provided", everywhere.** Go's `encoding/json` cannot distinguish an omitted key from `""` or `null` in a non-pointer field, and many models cannot omit a declared property at all — they emit every key and blank the unused ones. Three rules follow, and all three are load-bearing:

- **A parameter description must never forbid the empty string.** "Do not pass an empty string / omit entirely" is unsatisfiable for a model that can't omit fields, and pushes it toward sentinels like `"default"` or `"none"` that then get looked up as real values and fail. State the empty-string semantics instead — `web_search`'s *"Search source. Empty to see guide."* is the model to copy. dispatch's `agent` description used to say the opposite and was the exact trap it warned about.
- **`required:"true"` means present AND non-empty.** It cannot express "required, but empty is legal" — for that the field must be a **pointer**. `write_file.Content` is `*string` for this reason: content is mandatory (a dropped key must not silently truncate the file to 0 bytes, which it did), yet `""` is the legitimate way to create an empty file. `edit_file.NewText` is `*string` for the same reason: a dropped key must not silently delete `old_text`, yet `""` is the legitimate way to delete it. Conversely `use_skill.Name` is deliberately *not* required, because an empty name lists the skills — tagging it required made that branch unreachable dead code.
- **Numeric params treat `<= 0` as "use the default".** A future numeric param where 0 is meaningful MUST be a pointer, or an omitted key silently means 0.

`parseArgs` applies alias rewriting and unknown-key rejection **recursively**, through nested objects and arrays, reporting a JSON path (`sends[0].delay`). A top-level-only guard is not enough: `dispatch`'s payload is an array of objects, and a dropped key there fails silently in a way the model reads as success (`delay` → the wake fires *now*). A type that implements `json.Unmarshaler` is opaque to this walk, so aliases are declared as struct tags rather than hand-rolled in `UnmarshalJSON`.

### edit_file is deliberately ONE replacement per call

`edit_file` briefly (v1.5.x, commit `1c752a9`, 2026-06-28) took an `edits[]` array — one call, N disjoint replacements, ported from pi-mono. **It was reverted, and the numbers are why.** Malformed-JSON rejections went from **1 in the 107 days before to 87 in the 16 days after**; total `edit_file` failures went from 1.97/day to 9.62/day. Batching hurt on both axes at once:

- **The payload got harder to emit.** A nested array of long CJK strings with escaped quotes is not the same task as a flat `{path, old_text, new_text}`. One stray full-width `”` where the closing `"` belongs kills the whole call in `parseArgs`, before any of the engine's fuzzy smart-quote tolerance can help — that tolerance runs on `old_text` *matching*, not on JSON *syntax*.
- **Failure compounds.** The engine matches every `old_text` against the ORIGINAL file, all-or-nothing (`applyEditsToNormalizedContent`), so one stale `old_text` out of N discards the N−1 that would have applied: per-call failure goes as `1-(1-p)^n`. Observed `old_text` mismatches roughly doubled per day.

The pi-mono **engine** (`tools/edit_diff.go` — fuzzy NFKC/smart-quote/dash normalization, CRLF and BOM preservation, uniqueness and overlap checks) is kept in full and still takes `[]editPair`; only the **tool boundary** is single. `edits[]` is now rejected by name as an unknown argument, so a model trained on the old shape is told to resend rather than silently making no edit. Do not reintroduce it without re-deriving these numbers.

### Audio Support

Audio recognition follows the same pattern as vision: `AudioModels` registered per provider, `SupportsAudio()` capability check, `<<media:audio/ogg:path>>` markers, and `audioreader` agent delegation for non-audio models.

- **Channel layer**: Telegram Voice/Audio and Discord audio attachments are downloaded to `{workspace}/media/` (same `downloadMedia()` as images).
- **Tool layer**: `DetectFileType` recognizes `FileTypeAudio` via extension + magic bytes. `handleAudio()` returns media marker if `SupportsAudio`, otherwise guides LLM to delegate to `audioreader`.
- **Provider layer**: OpenRouter sends audio markers as `input_audio` content parts. Gemini uses generic `inlineData`. Non-audio providers skip audio markers.
- **Token estimation**: `EstimateAudioTokens()` uses file size + bitrate heuristic, ~32 tokens/sec.
- **audioreader agent**: `specialty: [audio]`, configured during `onboard` (same flow as imagereader specialty routing).

### Sessions (`session/`)

Conversation history persisted as `{sessionsDir}/{sessionKey}/session.jsonl`. Auto-sanitized on save. Context pressure hooks trigger compression when token budget is exceeded.

### Web Auth (`auth/`, `channel/web_auth.go`)

The web UI is login-gated. The credential model has exactly two stages and no passwords: a **one-time login link** (`nagobot login-link`, 30 min, single use, minted via RPC — deliberately a hard CLI command, never an LLM tool) bootstraps a browser, and a **passkey** (WebAuthn, `go-webauthn` server-side, `@simplewebauthn/browser` client-side) is the durable login. Opening a link offers create-new-user or associate-existing-user (with second confirmation), then registers a passkey; lost passkey = new link + associate back to your username. Without a link, the page offers only "Sign in with passkey"; a spent/expired link shows invalid (HTTP 410, indistinguishable by design).

- **Person registry** (`{workspace}/system/persons.json`): one human across channels — `{id, username, identities: ["discord:1480..."], credentials}`. An identity belongs to at most one person; rebinding moves it. Trust model: all users are trusted (no channel-side verification of identity claims), the UI double-confirms.
- **Channel-identity dictionary** (`system/identities.json`): the Dispatcher records `channel:userID → display name` per real user message (`RecordIdentity` in `dispatch()`, user-visible sources only) so the associate flow can list "discord: Nansen". Name-only display; the stable platform user ID is the key.
- **Device sessions** (`system/web_sessions.json`): SHA-256 token hashes → person, 90-day TTL, sliding LastSeen persisted at ≥1h granularity. Cookie is HttpOnly SameSite=Lax. Login codes live in memory only — restart voids outstanding links.
- **IP exemption**: `channels.web.auth.exemptCidrs` is the ONLY exemption source and defaults to empty — there is no implicit loopback pass; a browser on the host must log in like anyone else (only `RemoteAddr` is consulted, forwarding headers never). Local curl/tooling that needs unauthenticated API access opts in with `exemptCidrs: ["127.0.0.0/8"]` (Docker NATs host requests to the bridge IP, so containers may need `172.16.0.0/12` instead). `auth.disabled: true` turns the gate off.
- **`web-login` skill**: the LLM can mint links on explicit user request (`login-link` via exec); the skill forbids posting links into group channels.
- **Group viewing**: WS binding is per session AND per viewer (`clients[session][viewerKey]`, viewerKey = person ID or a shared `"anon"` for exempt/auth-off connections). A person's newer page displaces their older one (close code 4001); different persons coexist on the same session — Send/StreamTo multicast to every bound page, and an inbound message is broadcast to the other viewers as a `peer_message` frame (they would otherwise only see the reply stream). Participants (persons who ever SENT a message in the session via web, persisted in `system/web_session_members.json`) get person-filtered push when not watching; sessions with no participant record keep the legacy broadcast-push-when-nobody-connected behavior. Person-less push subscriptions are never matched by filtered sends.
- **WebAuthn constraints**: RP ID must be a domain (default `localhost`), origins default to `http://localhost:{port}` + `127.0.0.1`. Passkeys only exist in secure contexts — remote deployments need HTTPS on a real hostname (e.g. `tailscale serve`) and matching `auth.rpId`/`auth.origins`/`auth.publicUrl`. Registration requires resident keys so login is usernameless (`FinishDiscoverableLogin` maps the user handle = person ID).
- All auth endpoints under `/api/auth/*` are public (they are the door); everything else — `/api/*` and `/ws` — goes through `protected()`.

## Session vs Thread — Critical Distinction

**Session** = persistent on-disk data (`session.jsonl`, `heartbeat.md`). Survives restarts, lives indefinitely.

**Thread** = transient in-memory execution unit. Created by `Manager.NewThread()`, GC'd after 3h idle. `NewThread()` initializes `lastUserActiveAt = time.Now()` — this is NOT a reliable indicator of when the user was actually last active. For accurate user activity timestamps, always scan `session.jsonl` (via `collectSessions` or `isRealUserSource`), not in-memory thread state.

**Rule**: Any scheduling or timing logic (heartbeat, compression eligibility) that needs `lastUserActiveAt` for sessions that may have been GC'd MUST read from `session.jsonl`, not from `Thread.lastUserActiveAt`. Threads are ephemeral — their state is lost on GC and reset on recreation.

## Heartbeat System (`cmd/heartbeat_scheduler.go`)

The heartbeat runs background maintenance between user interactions. Heartbeat turns themselves never message the user — output is limited to background writes (USER.md via session-reflect; dream.md, the session's `sessions_summary.json` entry via `set-summary` when stale, its `memory/*.md` summary frontmatter via `set-memory-summary`, and file-track via dream) plus silent no-op pulses. The one indirect exception: dream may schedule a single next-day follow-up (`cron set-at --direct-wake` into its own session, high-suitability candidates only); when that wake fires the next day it is a **cron** wake, so the session sends the follow-up by simply writing it as plain text, or silently drops it with `dispatch({})` if the moment has passed.

### Architecture

A Go goroutine (`heartbeatScheduler`) scans every 30s and fires heartbeat pulses into user sessions. NOT a cron job — the old cron-based dispatcher was removed.

**Most pulses wake nothing.** `maybeFirePulse` calls `mgr.Wake()` only when `pulseIndex == hbReflectPulse || dream` — every other pulse advances `lastPulse`, logs `heartbeat pulse fired`, and costs **zero LLM calls**. (The log line is misleading: it prints even when no thread was woken.)

When a pulse *does* wake the thread, it runs `use_skill("heartbeat-wake")`, a router (`cmd/templates/skills/heartbeat-wake/SKILL.md`). The payload carries `pulse_index`, `elapsed_since_user`, `next_pulse`, and (only on dream pulses) `should_dream: true`. Routing, checked in order:

- **`should_dream: true`** → `use_skill("dream")` — review the past 24h, overwrite `dream.md`, refresh the session summary only if it has gone stale, summarize this session's unsummarized `memory/*.md` files (≤3), then run file-track. The scheduler sets this only at session-local night (02:00–06:00), user quiet long, `pulse_index > 2`, and ≥4h since the last dream (`shouldDream`, dedup via `dream_log.jsonl`).
- **`pulse_index == 4`** (`hbReflectPulse`, = 4h00m of user quiet; and not a dream) → `use_skill("session-reflect")` — extract user preferences/corrections/patterns into `USER.md`.
- **anything else** → `dispatch({})`. This branch is **unreachable** — the scheduler never wakes on those pulses. It exists only as a guard.

The pulse number lives in two places — `hbReflectPulse` in Go and the routing table in the skill markdown. Changing one without the other silently disables reflect.

Both live paths end silently — heartbeat never produces user-facing output. They are also the system's single largest token consumer: measured over 14 days, heartbeat was 47.9% of all prompt tokens (1.85x every real Discord conversation combined) for zero user-facing output. Two known sources of waste remain inside the turns: `session-reflect` calls `read_file(USER.md)` even though `buildUserSection` (`thread/run.go`) already injects USER.md **in full** into the system prompt, and it fans out into repeated `edit_file` calls instead of the single `write_file` the skill asks for. Each such call re-sends the entire session context.

### Timing

- **Quiet threshold**: 15 min after last user message (`hbQuietMin`)
- **Pulse interval**: 45 min base, +30 min each cycle (`hbPulseInterval`, `hbPulseGrowth`)
- **Activity window**: 48h — stops pulsing if no user activity within 48h (~21 pulses max)
- **Schedule**: `lastActive+15m, +60m, +135m, +240m, +375m, ...` (15 min first pulse, then 45/75/105/135/... growing gaps). Pulse 4 = `+240m` = 4h00m — the `hbReflectPulse` that fires session-reflect.
- **`pulse_index` resets to 1 on every user message.** It measures *continuous quiet*, not elapsed wall-clock. A user who speaks every 2h never reaches pulse 4, so no reflect fires for that session that day — this is intended (reflect should run when the conversation is over, not during a lunch break), but it means pulse index can never express a "at most once every N hours" cooldown.

### Critical Implementation Details

**Trigger timeline**: The pulse schedule is derived from `lastActive` (user's last message), NOT from `lastPulse`. `latestDueTrigger(lastActive, now)` returns `(trigger, nextInterval)` by iterating growing intervals: `lastActive+15m, +60m, +135m, +240m, ...`. A pulse fires only when the latest trigger point > `lastPulse`. This means `lastPulse` is purely a dedup guard — it prevents re-firing within the same cycle but never determines when the next pulse should be.

**State persistence**: `lastPulse` is persisted to `{workspace}/system/heartbeat-state.json`. State survives restarts — no cold-start alignment logic needed.

**User activity source**: The scheduler uses `collectSessions()` (scans `session.jsonl` for `isRealUserSource`) to get accurate `lastUserActiveAt`. It does NOT use `Thread.lastUserActiveAt` because threads initialize this to `time.Now()` on creation, which would make heartbeat-created threads appear "just active."

**`heartbeat status` RPC**: The CLI command calls `heartbeat.status` RPC which reads the scheduler's persisted state (`lastPulse`, computed intervals). It does NOT compute independently — it reflects real scheduler state.

### CLI Commands

- `nagobot heartbeat status` — show real next pulse times from live scheduler (via RPC)
- `nagobot heartbeat postpone <session-key> <duration>` — delay pulses for a session

## Compression (`thread/compress.go`)

### Tier 1 — Mechanical (idle ≥5 min, always runs)

- Tool result compression (use_skill → header-only if outdated/old)
- Wake payload compression (strip redundant YAML fields)
- Body compression (large system-sender content → head+tail)
- **Heartbeat turn trim**: marks entire heartbeat turns for removal if `isHeartbeatSkipTurn` returns true:
  - Requires `dispatch({})` was called (tool result contains `turn-terminated-silent` outcome)
  - Turns that delivered via dispatch (to=caller:session / to=session) are PRESERVED
- Reasoning trim (>2h old reasoning content excluded at send-time)

### Tier 2 — AI-driven (idle ≥30 min, tokens >70%)

Wakes thread with `WakeCompression` source, loads `context-ops` skill to summarize.

### Source Matching

Heartbeat source matching uses `strings.HasPrefix(source, "heartbeat")` to cover both new (`"heartbeat"`) and old (`"heartbeat_reflect"`, `"heartbeat_wake"`) source strings in existing sessions.

## Key Patterns

- **Hot-reload config**: Provider keys use `KeyFn` closures that call `config.Load()` each invocation. `Available()` checks at call time, not registration time. Channels (Telegram/Discord/Feishu) are hot-reloaded every 10s — adding a token to config auto-starts the channel.
- **Per-wake sink**: Each WakeMessage carries its own Sink callback for response delivery. Zero Sink falls back to thread default.
- **Agent override**: `WakeMessage.AgentName` overrides the thread's agent for that turn only.
- **Async child threads**: `SpawnChild()` is fully async. Child completion wakes parent via Sink → Enqueue.
- **Template workspace**: Canonical templates live in `cmd/templates/`. `onboard --sync` copies to `~/.nagobot/workspace/`. `cleanAndCopyEmbeddedDir` removes deleted templates. Never edit workspace files directly.
- **Default cron seeds**: `tidyup` (4am weekly), `world-knowledge` (midnight daily), `people-knowledge` (2am daily). Heartbeat is NOT a cron job. The old `session-summary` and `memory-summary` cron/agent/skill sets were both removed — **every per-session summary is now written by that session's own nightly dream**: the `sessions_summary.json` entry via `set-summary`, and the `memory/*.md` frontmatter via `list-memory-files --session` + `set-memory-summary`. Cron sessions no longer get summary refreshes (their last entry ages out via the 7-day cleanup once inactive).

### Memory summaries moved from one global cron into each session's dream

A `memory/YYYY-MM-DD.md` with no `summary:` frontmatter is **invisible**: `buildMemoryIndexSection` skips it entirely, so it appears in no `memory_index` and no prompt. Filling that frontmatter used to be `cron:memory-summary` (midnight daily, one global agent, `memory-summary-dispatcher` skill) sweeping every session's files. That design had a permanent hole: `list-memory-files` bounded its global walk at 30 days, so any file that aged past the window before being summarized could never be listed again. Measured on the deployment when this changed: 129 memory files, 114 summarized, and **all 15 unsummarized ones dated March 2026** — the month before `list-memory-files` shipped (2026-04-10, `ccd1241`). They were unreachable by every documented path, silently, with no error.

Now the session that owns the conversation pays its own debt, as step 6 of `dream`. Consequences:

- **The 30-day cutoff is gone**, which is what lets the March backlog finally drain. It only ever existed to bound the cron's global sweep; with `--session {{SESSIONKEY}}` the scan is one directory. The `maxMemoryFiles = 3` cap and the skip-today rule stay — the cap bounds one dream turn's cost and a backlog drains over consecutive nights; today's file is skipped because a later compression may still append to it.
- **Coupling to the dream schedule is deliberate and has a real edge**: a session that never dreams (heartbeat needs session-local night 02:00–06:00, long user quiet, `pulse_index > 2`, ≥4h since the last dream) never summarizes its memory files either. That is the intended trade — the old cron ran nightly whether or not anything needed doing, and the sessions that don't dream are the inactive ones whose files nobody will look up.
- `list-memory-files` keeps its unscoped global mode for ops inspection; nothing calls it automatically any more.

### The dream's session summary is now conditional

`set-summary` used to run every dream night unconditionally ("Refresh it every dream night, even when the past 24 hours were quiet"). It now runs only when the current summary has gone stale. The judgement needs no new tooling: `{{SESSIONS_SUMMARY}}` (`agent/agent.go:buildSessionsSummary` → the `active-sessions.md` section) filters out `:threads:` keys and nothing else, so **a session's own row is already in its own system prompt** as `- {{SESSIONKEY}}: …`. The skill lists the stale triggers (title no longer names the session / state it describes has changed / the past 24h changed what the session is *about* / no row at all) and otherwise says write nothing.

### There is no memory-search command

`search-memory` was deleted. It was never what its name and `Short` claimed ("Search across all session messages **and memory files**") — `mergeSessionMessages` read only `session.jsonl` + `history/*.jsonl` and never opened a `memory/*.md`, while `how-nagobot-works.md` simultaneously told the model to "prefer `nagobot search-memory`" *to locate memory files*. Its other defaults fought the one question it existed to answer: `--days 30` silently excluded any session quiet for over a month, and matching was plain substring with AND semantics over 50K+ messages (~2.9s per full-tree call, no index).

The observed failure that ended it: asked in a fresh web session when the user last renewed their passport, the bot dispatched a subagent that made **14 `search-memory` calls** (all `--days 90`, ~40s of wall clock), then reached for `grep` on `*.md` on its own — the only call in the whole turn that touched a memory file, and it returned 81 matches immediately.

So the replacement is `grep`, documented in `session-ops` (the "Recalling something from a past conversation" section), and nothing else — no new tool. What makes that work:

- **`memory/*.md` is the right corpus, not `session.jsonl`.** It is every session's whole past, already distilled, and `context-ops` writes a fixed 7-section report whose section 5 (*All user messages*) keeps the user's own sentences near-verbatim with dates.
- **Restrict by filename, never by path**: `include: "20??-??-??.md"`. The `grep` tool maps `include` to `rg --glob` when ripgrep exists and `grep --include=` otherwise, and GNU grep's `--include` matches the **basename only** — a path glob like `**/memory/*.md` silently matches nothing on a host without `rg` (the container has `/usr/bin/rg`; the native Tencent install may not). Dated basenames are the one form both backends agree on, and they hit memory files exclusively — `dream.md` / `USER.md` / `file-track.md` don't start with `20`. Verified against both backends: identical file lists, zero non-memory matches.
- **The two backends had to be made to speak the same regex dialect first.** `buildGrepArgs` ran bare `grep -rn`, i.e. BRE, where `|` and `(` are literal — so the alternation pattern the tool description and this skill both tell the model to write (`护照|passport`) matched **0 lines** on a host without ripgrep, while rg matched 39. Silent, and indistinguishable from "never mentioned" — the exact wrong conclusion this whole rewrite exists to prevent. The fallback is now `grep -rnE`; `TestGrepAlternationMatchesBothBranches` runs end-to-end through whichever backend is present, and passes with `rg` removed from `PATH`.
- **Two levels, and the skill says which answers what.** `grep "^summary:"` over the same file set is a *global* memory index — one line per file, ~113 lines over 21 sessions on the live workspace — and is the global counterpart to the per-session `memory_index` in the prompt. It finds WHERE (which session, which period); it cannot find WHAT, because a ~200-char summary does not carry the one date or number the user said once. So an empty index sweep is explicitly documented as **not** evidence of absence; only the content grep can conclude that.
- The skill's load-bearing instruction is **one regex with alternation instead of N calls** (`护照|passport|passeport`), because the 14-call sweep above is the exact failure it prevents, plus: two or three well-formed empty patterns is a real answer, not a cue to try a fourth phrasing.
- `memory_index` covers **only this session, only the last 20, only summarized files** — so its silence is not evidence, and cross-session recall *requires* the grep. That sentence is in the skill because the passport turn concluded "never mentioned" from an empty prompt.
- Deleting the command also rewrote the recovery hints embedded in every compressed message (`thread/compress.go`: `compressedHintFmt` / `compressedHintNoID`), which pointed at `search-memory --context <id>`. They now point at `history/*.jsonl` (a message ID greps verbatim there) and `memory/`, and defer to the skill for recipes — shorter than what they replaced, which matters because they ride along in every compressed message.
- **Prompt caching requires deterministic serialization**: All LLM providers use prefix-based prompt caching (tools → system → messages). Go map iteration is non-deterministic, so any map-derived output that ends up in the LLM request MUST be sorted. Currently sorted: `tools.Registry.Defs()`, `skills.Registry.List()`, `skills.Registry.SkillNames()`, `agent.buildSessionsSummary()`. When adding new map-iterated content to the system prompt or tools array, always sort the output.
- **Cache monitoring**: `provider.Usage.CachedTokens` flows through `Runner.totalUsage` → `monitor.TurnRecord` → `nagobot monitor --metrics` (per-provider `cacheHitRate`). All providers fill this field from their respective API response (OpenRouter/Moonshot/Zhipu/Minimax/xAI/SiliconFlow: `PromptTokensDetails.CachedTokens`; DeepSeek: `PromptCacheHitTokens`; Anthropic: `CacheReadInputTokens`; OpenAI: `InputTokensDetails.CachedTokens`; Gemini: `CachedContentTokenCount`).
- **Cache *write* monitoring**: `provider.Usage.CacheWriteTokens` rides the same path and surfaces as `cacheWriteTokens` in `monitor --metrics`. It matters because **gpt-5.6 bills a cache write at 1.25x the uncached input rate** — on a cold prefix it is the most expensive line of the request. Unlike `CachedTokens` it is summed for every provider, not gated on `isCacheUnreliable`: a raw count needs no matched denominator, so an unreliable provider cannot skew it.

  **It is always 0 on `openai-oauth`, and that is the backend's doing, not a parse bug.** The ChatGPT/Codex backend does not report cache writes at all. Confirmed twice: a 177K-token cold turn (`cachedTokens=0` — i.e. a guaranteed full cache write) returned no cache-write field; and OpenClaw, the reference implementation for that backend, reads only `inputTokens` / `cachedInputTokens` / `outputTokens` / `totalTokens` from the Codex `TokenUsageBreakdown` (`extensions/codex/src/app-server/event-projector.ts:normalizeCodexTokenUsage`) — there is no write counter to read. Its docs state it outright: *"Expect `cacheRead` only; `cacheWrite` stays `0`."* **So the 1.25x charge on openai-oauth is not auditable from usage data.** The parse is kept because `api.openai.com`'s Responses API *does* populate `input_tokens_details.cache_write_tokens`.

  **Arithmetic trap**: OpenAI's `input_tokens` *includes* both the cached and the cache-write buckets. Truly uncached (1.0x) input is `PromptTokens - CachedTokens - CacheWriteTokens`. `PromptTokens - CachedTokens` is only correct while `CacheWriteTokens` is 0.

### gpt-5.6 context window is a price decision, not a capacity one

`provider.gpt56ContextWindow` = **272000**, not the family's real 372K capacity. OpenAI bills a gpt-5.6 request whose input exceeds 272K at **2x input / 1.5x output for the entire request**. Since the registered window is what drives compression and the context budget trim (Tier 2 at 70%, Tier 3 at 85%, trim at `min(92%, window − maxTokens − 10K)`), registering 372000 would let requests grow to **~342K** — silently double-billing everything in the 272K–342K band. At 272000 the trim line caps requests at ~245K (Tier 2 fires at 190,400), and crossing the band is structurally impossible. `TestGPT56ContextWindowStaysUnderPriceBreak` asserts the math rather than the constant, so "restoring the real capacity" fails loudly. This is the same defect Codex itself ships: openai/codex#32486.

Note gpt-5.5 needs no such cap: on `openai-oauth` its window is already 272000 (the Codex plan's own limit), and on the API-key `openai` route its 1M window carries no 272K surcharge.

## Common Pitfalls

- **Don't trust `Thread.lastUserActiveAt` for scheduling**: It's initialized to `time.Now()` on thread creation, not actual user activity. Use `collectSessions()` → `LastUserActiveAt` from `session.jsonl` scan.
- **Don't use `logger.Debug` for things you need to see**: Heartbeat scheduler activity, error conditions — use `Info` or `Warn`. Debug is invisible at default log level.
- **Heartbeat state is persisted**: `lastPulse` is saved to `heartbeat-state.json` after each pulse. Restarts reload this state — no cold-start special-casing needed.
- **`collectSessions` loads full session data**: Every call parses entire `session.jsonl` for all matching sessions. Don't call it in tight loops. The scheduler calls it every 30s — acceptable for small deployments.
- **`{{WORKSPACE}}` resolves in both agents and skills**: `agent.Build()` and `use_skill` (`tools/skills.go`) both replace `{{WORKSPACE}}`. Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.
- **Heartbeat turns suppress via LLM, not code**: The old `WakeHeartbeatReflect` had code-level `SetSuppressSink()`. Now both reflect and act use `WakeHeartbeat` — suppression relies on the LLM calling `dispatch({})`. If the LLM forgets, output leaks to the user.
- **`applyDefaults()` only adds, never prunes**: If a cron seed is removed from `defaultCronSeeds()`, old entries in `config.yaml` persist. Manual cleanup may be needed after upgrades.

## Deployment

Install: `curl -fsSL https://nagobot.com/install.sh | bash` (all platforms). Update: `nagobot update`.

Service managed via launchd (macOS), systemd (Linux), or Task Scheduler (Windows). Logs at `~/.nagobot/logs/`.

Release pipeline: push `v*` tag → GitHub Actions builds all platform binaries (linux/darwin/windows) → publishes to GitHub Releases.
