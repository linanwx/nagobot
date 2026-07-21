"use client";

import {
  ComposerAddAttachment,
  ComposerAttachments,
  UserMessageAttachments,
} from "@/components/assistant-ui/attachment";
import { ThreadFollowupSuggestions } from "@/components/assistant-ui/follow-up-suggestions";
import { MarkdownText } from "@/components/assistant-ui/markdown-text";
import {
  Reasoning,
  ReasoningContent,
  ReasoningRoot,
  ReasoningText,
  ReasoningTrigger,
} from "@/components/assistant-ui/reasoning";
import { ToolFallback } from "@/components/assistant-ui/tool-fallback";
import {
  ToolGroupContent,
  ToolGroupRoot,
  ToolGroupTrigger,
} from "@/components/assistant-ui/tool-group";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { Button } from "@/components/ui/button";
import { useCoarsePointer } from "@/hooks/use-coarse-pointer";
import type { MessageMeta } from "@/hooks/use-nagobot-chat";
import { mediaURL } from "@/lib/api";
import { formatMessageTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";
import {
  ActionBarMorePrimitive,
  ActionBarPrimitive,
  AuiIf,
  type AssistantState,
  BranchPickerPrimitive,
  ComposerPrimitive,
  ErrorPrimitive,
  groupPartByType,
  MessagePrimitive,
  SuggestionPrimitive,
  ThreadPrimitive,
  type ToolCallMessagePartComponent,
  useAuiState,
} from "@assistant-ui/react";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CheckIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  CopyIcon,
  DownloadIcon,
  MicIcon,
  MoreHorizontalIcon,
  SquareIcon,
} from "lucide-react";
import {
  createContext,
  useContext,
  useState,
  type ComponentType,
  type FC,
  type PropsWithChildren,
} from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

export type ThreadGroupPart = MessagePrimitive.GroupedParts.GroupPart;

/**
 * Optional component overrides for the thread. `AssistantMessage` and
 * `Welcome` replace whole sections; the remaining slots override how the
 * assistant message renders tool calls and part groups. Tool UIs registered
 * by name (toolkit `render`, `useAssistantDataUI`) take precedence over
 * `ToolFallback`.
 */
export type ThreadComponents = {
  AssistantMessage?: ComponentType | undefined;
  Welcome?: ComponentType | undefined;
  ToolFallback?: ToolCallMessagePartComponent | undefined;
  ToolGroup?:
    | ComponentType<PropsWithChildren<{ group: ThreadGroupPart }>>
    | undefined;
  ReasoningGroup?:
    | ComponentType<PropsWithChildren<{ group: ThreadGroupPart }>>
    | undefined;
};

export type ThreadProps = {
  components?: ThreadComponents | undefined;
};

const EMPTY_COMPONENTS: ThreadComponents = {};

const ThreadComponentsContext =
  createContext<ThreadComponents>(EMPTY_COMPONENTS);

// Startup exposes a loading placeholder thread; treat it as a new chat so
// the composer mounts centered. Loads after startup keep the docked layout.
const isNewChatView = (s: AssistantState) =>
  s.thread.messages.length === 0 &&
  (!s.thread.isLoading || s.threads.isLoading);

export const Thread: FC<ThreadProps> = ({ components = EMPTY_COMPONENTS }) => {
  const isEmpty = useAuiState(isNewChatView);

  return (
    <ThreadComponentsContext.Provider value={components}>
      <ThreadRoot isEmpty={isEmpty} />
    </ThreadComponentsContext.Provider>
  );
};

const ThreadRoot: FC<{ isEmpty: boolean }> = ({ isEmpty }) => {
  const { Welcome = ThreadWelcome } = useContext(ThreadComponentsContext);

  return (
    <ThreadPrimitive.Root
      className="aui-root aui-thread-root bg-background @container flex h-full flex-col"
      style={{
        ["--thread-max-width" as string]: "44rem",
        ["--composer-bg" as string]:
          "color-mix(in oklab, var(--color-muted) 30%, var(--color-background))",
        ["--composer-radius" as string]: "1.5rem",
        ["--composer-padding" as string]: "8px",
      }}
    >
      <ThreadPrimitive.Viewport
        turnAnchor="top"
        data-slot="aui_thread-viewport"
        className="relative flex flex-1 flex-col overflow-x-auto overflow-y-scroll scroll-smooth overscroll-contain"
      >
        <div
          className={cn(
            "mx-auto flex w-full max-w-(--thread-max-width) flex-1 flex-col px-4 pt-4",
            isEmpty && "justify-center",
          )}
        >
          <AuiIf condition={isNewChatView}>
            <Welcome />
          </AuiIf>

          <div
            data-slot="aui_message-group"
            className="mb-14 flex flex-col gap-y-6 empty:hidden"
          >
            <ThreadPrimitive.Messages>
              {() => <ThreadMessage />}
            </ThreadPrimitive.Messages>
          </div>

          <ThreadPrimitive.ViewportFooter
            className={cn(
              "aui-thread-viewport-footer bg-background flex flex-col gap-4 overflow-visible pb-[max(1rem,env(safe-area-inset-bottom))] md:pb-6",
              !isEmpty &&
                "sticky bottom-0 mt-auto rounded-t-(--composer-radius)",
            )}
          >
            <ThreadScrollToBottom />
            <ThreadFollowupSuggestions />
            <Composer />
            <AuiIf condition={(s) => isNewChatView(s) && s.composer.isEmpty}>
              <ThreadSuggestions />
            </AuiIf>
          </ThreadPrimitive.ViewportFooter>
        </div>
      </ThreadPrimitive.Viewport>
    </ThreadPrimitive.Root>
  );
};

// useMessageMeta reads the nagobot metadata (event kind, source, cross-session
// caller/target, speaker name) that convertMessage stores on each message.
const useMessageMeta = (): MessageMeta =>
  useAuiState((s) => s.message.metadata?.custom as MessageMeta | undefined) ??
  {};

// MessageTimestamp shows the message's createdAt compactly (time-only today,
// date + time otherwise). Renders nothing when the timestamp is missing.
const MessageTimestamp: FC<{ className?: string }> = ({ className }) => {
  const createdAt = useAuiState(
    (s) => s.message.createdAt as Date | undefined,
  );
  if (!createdAt) return null;
  const label = formatMessageTime(new Date(createdAt));
  if (!label) return null;
  return (
    <span
      data-slot="aui_message-timestamp"
      className={cn(
        "text-muted-foreground/70 text-[10px] tabular-nums",
        className,
      )}
    >
      {label}
    </span>
  );
};

const ThreadMessage: FC = () => {
  const { AssistantMessage: AssistantMessageComponent = AssistantMessage } =
    useContext(ThreadComponentsContext);
  const role = useAuiState((s) => s.message.role);
  const isEditing = useAuiState((s) => s.message.composer.isEditing);
  const customKind = useAuiState(
    (s) => (s.message.metadata?.custom as MessageMeta | undefined)?.kind,
  );

  if (customKind === "event") return <EventMessage />;
  if (customKind === "tool") return <ToolCardMessage />;
  if (isEditing) return <EditComposer />;
  if (role === "user") return <UserMessage />;
  return <AssistantMessageComponent />;
};

// CompressedChip marks content whose original was replaced by Tier-1
// compression — what's shown is the compressed version, not the full text.
const CompressedChip: FC = () => (
  <span className="rounded bg-violet-500/15 px-1 py-px text-[10px] tracking-wide text-violet-700 uppercase dark:text-violet-300">
    compressed
  </span>
);

// Tool card bodies (args / result) get a hard cap so a huge read_file result
// can't take over the thread even when expanded.
const toolBodyMaxChars = 6000;

function capText(text: string): string {
  if (text.length <= toolBodyMaxChars) return text;
  return text.slice(0, toolBodyMaxChars) + `\n… (${text.length} chars total)`;
}

// oneLinePreview compacts a text into a single header-line preview.
function oneLinePreview(text: string, max: number): string {
  const line = text.replace(/\s+/g, " ").trim();
  return line.length > max ? line.slice(0, max) + "…" : line;
}

// ToolCardMessage renders a single tool invocation as a collapsed one-line
// card — name plus an args preview — expanding to the full arguments and the
// paired result. This is the "everything else" bucket: any assistant activity
// that is not speech (and not a dispatch delivery) is a tool call, so the
// full trace stays visible without drowning the conversation.
const ToolCardMessage: FC = () => {
  const meta = useMessageMeta();
  const [expanded, setExpanded] = useState(false);
  const thinking = meta.toolName === "thinking";
  // Live thinking previews its trailing text (the freshest thought), other
  // live/idle cards preview args from the start.
  const previewSrc = thinking ? (meta.resultText ?? "") : (meta.argsText ?? "");
  const preview =
    thinking && meta.running
      ? "…" + previewSrc.replace(/\s+/g, " ").trim().slice(-100)
      : oneLinePreview(previewSrc, 100);
  return (
    // Negative vertical margins eat most of the thread's gap-y-6 so runs of
    // consecutive tool cards (agentic turns) stack tightly while chat
    // bubbles keep their roomier rhythm.
    <MessagePrimitive.Root
      data-slot="aui_tool-card-root"
      className="-my-2.5 px-2"
      data-role="tool"
    >
      <div className="border-border/25 text-muted-foreground w-full rounded-md border bg-transparent px-3 py-1.5 text-xs opacity-80">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex w-full items-center gap-2 text-start"
        >
          <span className={cn("shrink-0", meta.running && "animate-pulse")}>
            {thinking ? "💭" : "⚙"}
          </span>
          <span className="shrink-0 font-mono font-medium">
            {meta.toolName}
          </span>
          {meta.compressed ? <CompressedChip /> : null}
          <span className="text-muted-foreground/60 min-w-0 flex-1 truncate font-mono text-[11px]">
            {preview}
          </span>
          {meta.running ? (
            <span className="text-sky-600 dark:text-sky-400 shrink-0 animate-pulse text-[10px]">
              ●
            </span>
          ) : null}
          <MessageTimestamp />
          <span className="text-muted-foreground/60 shrink-0 text-[10px]">
            {expanded ? "▲" : "▼"}
          </span>
        </button>
        {expanded && (
          <div className="mt-1.5 flex flex-col gap-1.5">
            {meta.argsText ? (
              <pre className="bg-muted/60 max-h-60 overflow-auto rounded p-2 font-mono text-[11px] whitespace-pre-wrap">
                {capText(meta.argsText)}
              </pre>
            ) : null}
            {meta.resultText ? (
              <pre className="border-border/40 max-h-80 overflow-auto rounded border p-2 font-mono text-[11px] whitespace-pre-wrap">
                {capText(meta.resultText)}
              </pre>
            ) : null}
            {!meta.argsText && !meta.resultText ? (
              <span className="text-muted-foreground/60 text-[11px] italic">
                no arguments, no recorded result
              </span>
            ) : null}
          </div>
        )}
      </div>
    </MessagePrimitive.Root>
  );
};

// MediaAttachments renders a user message's attachments from the protected
// /api/media route: images inline, audio as a player, anything else as a
// download link. mediaPreview (the upfront AI description/transcription)
// renders as a dim caption.
const MediaAttachments: FC<{
  media: NonNullable<MessageMeta["media"]>;
  preview?: string | undefined;
}> = ({ media, preview }) => (
  <div className="flex flex-col items-end gap-1.5">
    {media.map((m) =>
      m.kind === "image" ? (
        <img
          key={m.name}
          src={mediaURL(m.name)}
          alt={m.name}
          loading="lazy"
          className="max-h-72 max-w-full rounded-xl border object-contain"
        />
      ) : m.kind === "audio" ? (
        <audio key={m.name} src={mediaURL(m.name)} controls className="max-w-full" />
      ) : (
        <a
          key={m.name}
          href={mediaURL(m.name)}
          target="_blank"
          rel="noreferrer"
          className="bg-muted text-foreground flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs underline-offset-2 hover:underline"
        >
          📎 {m.name}
        </a>
      ),
    )}
    {preview ? (
      <span className="text-muted-foreground/70 max-w-full text-[11px] italic">
        {preview}
      </span>
    ) : null}
  </div>
);

// Event bodies longer than this collapse by default. Text-length heuristic on
// purpose: it is stable before layout, so cards never resize after mount.
// (Message roots deliberately do NOT use content-visibility:auto — off-screen
// placeholder heights made scroll positions jump as real heights resolved.)
const eventCollapseChars = 300;
const eventCollapseLines = 5;

// Badge tint per event source, so the card kind is scannable at a glance:
// outgoing dispatches read blue, incoming session traffic green, cron amber,
// everything else neutral.
function eventBadgeClass(source: string | undefined): string {
  switch (source) {
    case "dispatch":
      return "bg-sky-500/15 text-sky-700 dark:text-sky-300";
    case "session":
      return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300";
    case "cron":
      return "bg-amber-500/15 text-amber-700 dark:text-amber-300";
    case "compression":
      return "bg-violet-500/15 text-violet-700 dark:text-violet-300";
    case "error":
      return "bg-red-500/15 text-red-700 dark:text-red-300";
    default:
      return "bg-muted";
  }
}

// Quiet sources are operational traces (subagent progress snapshots,
// heartbeat pulses, compression runs), not conversation: their card is
// dimmed and collapses to the one-line summary.
function isQuietEventSource(source: string | undefined): boolean {
  return (
    source === "progress" ||
    source === "tools" ||
    source === "compression" ||
    (source?.startsWith("heartbeat") ?? false)
  );
}

// Sources whose body is LLM-authored message text and therefore markdown;
// cron job specs and progress traces stay raw.
function isMarkdownEventSource(source: string | undefined): boolean {
  return source === "dispatch" || source === "session";
}

// shortenProgressSummary hides the full session key in a collapsed progress
// summary line, keeping only its last segment (the thread slug):
// "subagent discord:123:threads:foo · running 1m" → "subagent foo · running 1m".
function shortenProgressSummary(line: string): string {
  return line.replace(/\b(subagent|fork)\s+(\S+)/, (_m, kind, key) => {
    const parts = String(key).split(":");
    return `${kind} ${parts[parts.length - 1] || key}`;
  });
}

// Compact markdown for event-card bodies. The registry MarkdownText can't be
// used here: it renders through the assistant message part context, which
// events (often user-role frames) don't have.
const eventMarkdownComponents: Components = {
  h1: (p) => <h3 className="mt-2 mb-1 font-semibold first:mt-0" {...p} />,
  h2: (p) => <h3 className="mt-2 mb-1 font-semibold first:mt-0" {...p} />,
  h3: (p) => <h4 className="mt-2 mb-1 font-semibold first:mt-0" {...p} />,
  p: (p) => <p className="mb-1.5 last:mb-0" {...p} />,
  ul: (p) => <ul className="mb-1.5 list-disc ps-4 last:mb-0" {...p} />,
  ol: (p) => <ol className="mb-1.5 list-decimal ps-4 last:mb-0" {...p} />,
  blockquote: (p) => (
    <blockquote
      className="border-border/60 text-muted-foreground/70 mb-1.5 border-s-2 ps-2 italic"
      {...p}
    />
  ),
  a: (p) => (
    <a
      className="underline underline-offset-2"
      target="_blank"
      rel="noreferrer"
      {...p}
    />
  ),
  code: (p) => (
    <code className="bg-muted rounded px-1 font-mono text-[11px]" {...p} />
  ),
  pre: (p) => (
    <pre
      className="bg-muted mb-1.5 overflow-x-auto rounded p-2 font-mono text-[11px] last:mb-0"
      {...p}
    />
  ),
  hr: () => <hr className="border-border/40 my-2" />,
  table: (p) => (
    <div className="mb-1.5 overflow-x-auto last:mb-0">
      <table className="text-[11px]" {...p} />
    </div>
  ),
  th: (p) => (
    <th className="border-border/40 border px-1.5 py-0.5 text-start" {...p} />
  ),
  td: (p) => <td className="border-border/40 border px-1.5 py-0.5" {...p} />,
};

// splitLeadingQuote peels the "> Re: …" reply-quote lines off an event body so
// they can render as a dim quote block instead of blending into the content.
function splitLeadingQuote(text: string): { quote: string; body: string } {
  const lines = text.split("\n");
  let i = 0;
  while (i < lines.length && lines[i].startsWith(">")) i++;
  if (i === 0) return { quote: "", body: text };
  const quote = lines
    .slice(0, i)
    .map((l) => l.replace(/^>\s?/, ""))
    .join("\n");
  let j = i;
  while (j < lines.length && lines[j].trim() === "") j++;
  return { quote, body: lines.slice(j).join("\n") };
}

// EventMessage renders daemon-internal traffic (wake payloads, inter-session
// dispatches, provider errors) as a centered system notice instead of a chat
// bubble, so it cannot be mistaken for human or assistant speech. The header
// line carries the source, the cross-session direction (which session sent
// this / where it was sent), and the timestamp. Long bodies start collapsed —
// inter-session traffic (briefings, task descriptions) runs to hundreds of
// lines and would otherwise drown the actual conversation.
const EventMessage: FC = () => {
  const meta = useMessageMeta();
  const [expanded, setExpanded] = useState(false);
  const text = useAuiState((s) => {
    let t = "";
    for (const part of s.message.content) {
      if (part.type === "text") t += part.text;
    }
    return t;
  });
  const { quote, body } = splitLeadingQuote(text);
  const quiet = isQuietEventSource(meta.source);
  const bodyLines = body.split("\n");
  const collapsible = quiet
    ? bodyLines.length > 1 || body.length > 160
    : body.length > eventCollapseChars ||
      bodyLines.length > eventCollapseLines;
  const collapsed = collapsible && !expanded;
  // Quiet cards keep only their summary line when collapsed (session key
  // shortened to its thread slug); normal cards keep a few lines of content.
  const shownBody =
    quiet && collapsed
      ? shortenProgressSummary(bodyLines.find((l) => l.trim() !== "") ?? "")
      : body;
  const markdown = isMarkdownEventSource(meta.source);
  const direction = meta.target
    ? `→ ${meta.target}`
    : meta.caller
      ? `from ${meta.caller}`
      : null;
  return (
    <MessagePrimitive.Root
      data-slot="aui_event-message-root"
      className="-my-1 px-2"
      data-role="event"
    >
      <div
        className={cn(
          "text-muted-foreground w-full rounded-md border px-3 py-2 text-xs",
          quiet
            ? "border-border/25 bg-transparent opacity-75"
            : "border-border/40 bg-muted/40",
        )}
      >
        <div className="mb-1 flex flex-wrap items-center gap-x-2 gap-y-0.5">
          {meta.source ? (
            <span
              className={cn(
                "rounded px-1 py-px text-[10px] tracking-wide uppercase",
                eventBadgeClass(meta.source),
              )}
            >
              {meta.source}
            </span>
          ) : null}
          {direction ? (
            <span className="font-mono text-[10px] break-all">
              {direction}
            </span>
          ) : null}
          <MessageTimestamp className="ms-auto" />
        </div>
        {quote !== "" && (
          <div className="border-border/60 text-muted-foreground/60 mb-1 line-clamp-2 border-s-2 ps-2 whitespace-pre-wrap italic wrap-break-word">
            {quote}
          </div>
        )}
        <div
          className={cn(
            "wrap-break-word",
            !markdown && "whitespace-pre-wrap",
            !quiet && collapsed && "max-h-24 overflow-hidden",
          )}
        >
          {markdown ? (
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={eventMarkdownComponents}
            >
              {shownBody}
            </ReactMarkdown>
          ) : (
            shownBody
          )}
        </div>
        {collapsible && (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="text-muted-foreground/80 hover:text-foreground mt-1 text-[10px] font-medium"
          >
            {expanded ? "Show less ▲" : "Show more ▼"}
          </button>
        )}
      </div>
    </MessagePrimitive.Root>
  );
};

const ThreadScrollToBottom: FC = () => {
  return (
    <ThreadPrimitive.ScrollToBottom asChild>
      <TooltipIconButton
        tooltip="Scroll to bottom"
        variant="outline"
        className="aui-thread-scroll-to-bottom dark:border-border dark:bg-background dark:hover:bg-accent absolute -top-12 z-10 self-center rounded-full p-4 disabled:invisible"
      >
        <ArrowDownIcon />
      </TooltipIconButton>
    </ThreadPrimitive.ScrollToBottom>
  );
};

const ThreadWelcome: FC = () => {
  return (
    <div className="aui-thread-welcome-root mb-6 flex flex-col items-center px-4 text-center">
      <h1 className="aui-thread-welcome-message-inner fade-in slide-in-from-bottom-1 animate-in fill-mode-both text-2xl font-semibold duration-200">
        How can I help you today?
      </h1>
    </div>
  );
};

const ThreadSuggestions: FC = () => {
  return (
    <div className="aui-thread-welcome-suggestions flex w-full flex-wrap items-center justify-center gap-2 px-4">
      <ThreadPrimitive.Suggestions>
        {() => <ThreadSuggestionItem />}
      </ThreadPrimitive.Suggestions>
    </div>
  );
};

const ThreadSuggestionItem: FC = () => {
  return (
    <div className="aui-thread-welcome-suggestion-display fade-in slide-in-from-bottom-2 animate-in fill-mode-both duration-200">
      <SuggestionPrimitive.Trigger send asChild>
        <Button
          variant="ghost"
          className="aui-thread-welcome-suggestion text-foreground hover:bg-muted border-border/60 h-auto gap-1.5 rounded-full border px-3.5 py-1.5 text-sm font-normal whitespace-nowrap transition-colors"
        >
          <SuggestionPrimitive.Title className="aui-thread-welcome-suggestion-text-1" />
          <SuggestionPrimitive.Description className="aui-thread-welcome-suggestion-text-2 empty:hidden" />
        </Button>
      </SuggestionPrimitive.Trigger>
    </div>
  );
};

const Composer: FC = () => {
  // On touch devices auto-focus pops the software keyboard, so every focus
  // trigger (mount, scroll-to-bottom arrow, run start, thread switch) must
  // stay off — the user taps the input when they actually want to type.
  const coarsePointer = useCoarsePointer();
  return (
    <ComposerPrimitive.Root className="aui-composer-root relative flex w-full flex-col">
      <ComposerPrimitive.AttachmentDropzone asChild>
        <div
          data-slot="aui_composer-shell"
          className="border-border/60 data-[dragging=true]:border-ring focus-within:border-border dark:border-muted-foreground/15 dark:focus-within:border-muted-foreground/30 flex w-full flex-col gap-2 rounded-(--composer-radius) border bg-(--composer-bg) p-(--composer-padding) shadow-[0_4px_16px_-8px_rgba(0,0,0,0.08),0_1px_2px_rgba(0,0,0,0.04)] transition-[border-color,box-shadow] focus-within:shadow-[0_6px_24px_-8px_rgba(0,0,0,0.12),0_1px_2px_rgba(0,0,0,0.05)] data-[dragging=true]:border-dashed data-[dragging=true]:bg-[color-mix(in_oklab,var(--color-accent)_50%,var(--color-background))] dark:shadow-none"
        >
          <ComposerAttachments />
          <ComposerPrimitive.Input
            placeholder="Send a message..."
            className="aui-composer-input caret-primary placeholder:text-muted-foreground/80 max-h-32 min-h-10 w-full resize-none bg-transparent px-2.5 py-1 text-base outline-none"
            rows={1}
            autoFocus={!coarsePointer}
            enterKeyHint="send"
            aria-label="Message input"
          />
          <ComposerAction />
        </div>
      </ComposerPrimitive.AttachmentDropzone>
    </ComposerPrimitive.Root>
  );
};

const ComposerAction: FC = () => {
  return (
    <div className="aui-composer-action-wrapper relative flex items-center justify-between">
      <ComposerAddAttachment />
      <div className="flex items-center gap-1.5">
        <AuiIf condition={(s) => s.thread.capabilities.dictation}>
          <AuiIf condition={(s) => s.composer.dictation == null}>
            <ComposerPrimitive.Dictate asChild>
              <TooltipIconButton
                tooltip="Voice input"
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-dictate size-7 rounded-full"
                aria-label="Start voice input"
              >
                <MicIcon className="aui-composer-dictate-icon size-4" />
              </TooltipIconButton>
            </ComposerPrimitive.Dictate>
          </AuiIf>
          <AuiIf condition={(s) => s.composer.dictation != null}>
            <ComposerPrimitive.StopDictation asChild>
              <TooltipIconButton
                tooltip="Stop dictation"
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-stop-dictation text-destructive size-7 rounded-full"
                aria-label="Stop voice input"
              >
                <SquareIcon className="aui-composer-stop-dictation-icon size-3.5 animate-pulse fill-current" />
              </TooltipIconButton>
            </ComposerPrimitive.StopDictation>
          </AuiIf>
        </AuiIf>
        <AuiIf condition={(s) => !s.thread.isRunning}>
          <ComposerPrimitive.Send asChild>
            <TooltipIconButton
              tooltip="Send message"
              side="bottom"
              type="button"
              variant="default"
              size="icon"
              className="aui-composer-send size-7 rounded-full"
              aria-label="Send message"
            >
              <ArrowUpIcon className="aui-composer-send-icon size-4.5" />
            </TooltipIconButton>
          </ComposerPrimitive.Send>
        </AuiIf>
        <AuiIf condition={(s) => s.thread.isRunning}>
          <ComposerPrimitive.Cancel asChild>
            <Button
              type="button"
              variant="default"
              size="icon"
              className="aui-composer-cancel size-7 rounded-full"
              aria-label="Stop generating"
            >
              <SquareIcon className="aui-composer-cancel-icon size-3.5 fill-current" />
            </Button>
          </ComposerPrimitive.Cancel>
        </AuiIf>
      </div>
    </div>
  );
};

const MessageError: FC = () => {
  return (
    <MessagePrimitive.Error>
      <ErrorPrimitive.Root className="aui-message-error-root border-destructive bg-destructive/10 text-destructive dark:bg-destructive/5 mt-2 rounded-md border p-3 text-sm dark:text-red-200">
        <ErrorPrimitive.Message className="aui-message-error-message line-clamp-2" />
      </ErrorPrimitive.Root>
    </MessagePrimitive.Error>
  );
};

const AssistantMessage: FC = () => {
  const {
    ToolFallback: ToolFallbackComponent = ToolFallback,
    ToolGroup,
    ReasoningGroup,
  } = useContext(ThreadComponentsContext);
  const meta = useMessageMeta();
  const createdAt = useAuiState(
    (s) => s.message.createdAt as Date | undefined,
  );

  const ACTION_BAR_PT = "pt-1.5";
  // Keep the action bar inside the contained root's paint box, then cancel its reserved space in flow.
  const ACTION_BAR_HEIGHT = `min-h-7.5 ${ACTION_BAR_PT}`;

  return (
    <MessagePrimitive.Root
      data-slot="aui_assistant-message-root"
      data-role="assistant"
      className="fade-in slide-in-from-bottom-1 animate-in relative -mb-7.5 pb-7.5 duration-150"
    >
      {meta.caller || meta.compressed || createdAt ? (
        <div
          data-slot="aui_assistant-message-header"
          className="flex items-baseline gap-2 px-2 pb-0.5"
        >
          {meta.caller ? (
            <span className="text-muted-foreground/70 font-mono text-[10px] break-all">
              via {meta.caller}
            </span>
          ) : null}
          {meta.compressed ? <CompressedChip /> : null}
          <MessageTimestamp />
        </div>
      ) : null}
      <div
        data-slot="aui_assistant-message-content"
        className="text-foreground px-2 leading-relaxed wrap-break-word"
      >
        <MessagePrimitive.GroupedParts
          groupBy={groupPartByType({
            reasoning: ["group-chainOfThought", "group-reasoning"],
            "tool-call": ["group-chainOfThought", "group-tool"],
            "standalone-tool-call": [],
          })}
        >
          {({ part, children }) => {
            switch (part.type) {
              case "group-chainOfThought":
                return <div data-slot="aui_chain-of-thought">{children}</div>;
              case "group-tool":
                if (ToolGroup) {
                  return <ToolGroup group={part}>{children}</ToolGroup>;
                }
                return (
                  <ToolGroupRoot variant="ghost">
                    <ToolGroupTrigger
                      count={part.indices.length}
                      active={part.status.type === "running"}
                    />
                    <ToolGroupContent>{children}</ToolGroupContent>
                  </ToolGroupRoot>
                );
              case "group-reasoning": {
                if (ReasoningGroup) {
                  return (
                    <ReasoningGroup group={part}>{children}</ReasoningGroup>
                  );
                }
                const running = part.status.type === "running";
                return (
                  <ReasoningRoot streaming={running}>
                    <ReasoningTrigger active={running} />
                    <ReasoningContent aria-busy={running}>
                      <ReasoningText>{children}</ReasoningText>
                    </ReasoningContent>
                  </ReasoningRoot>
                );
              }
              case "text":
                return <MarkdownText />;
              case "reasoning":
                return <Reasoning {...part} />;
              case "tool-call":
                return part.toolUI ?? <ToolFallbackComponent {...part} />;
              case "data":
                return part.dataRendererUI;
              case "indicator":
                return (
                  <span
                    data-slot="aui_assistant-message-indicator"
                    className="animate-pulse font-sans"
                    aria-label="Assistant is working"
                  >
                    {"●"}
                  </span>
                );
              default:
                return null;
            }
          }}
        </MessagePrimitive.GroupedParts>
        <MessageError />
      </div>

      <div
        data-slot="aui_assistant-message-footer"
        className={cn("ms-2 flex items-center", ACTION_BAR_HEIGHT)}
      >
        <BranchPicker />
        <AssistantActionBar />
      </div>
    </MessagePrimitive.Root>
  );
};

const AssistantActionBar: FC = () => {
  return (
    <ActionBarPrimitive.Root
      hideWhenRunning
      autohide="not-last"
      className="aui-assistant-action-bar-root text-muted-foreground animate-in fade-in col-start-3 row-start-2 -ms-1 flex gap-1 duration-200"
    >
      <ActionBarPrimitive.Copy asChild>
        <TooltipIconButton tooltip="Copy">
          <AuiIf condition={(s) => s.message.isCopied}>
            <CheckIcon className="animate-in zoom-in-50 fade-in duration-200 ease-out" />
          </AuiIf>
          <AuiIf condition={(s) => !s.message.isCopied}>
            <CopyIcon className="animate-in zoom-in-75 fade-in duration-150" />
          </AuiIf>
        </TooltipIconButton>
      </ActionBarPrimitive.Copy>
      {/* Reload removed — regenerating a reply is not supported by the
          nagobot backend (the session log is append-only). */}
      <ActionBarMorePrimitive.Root>
        <ActionBarMorePrimitive.Trigger asChild>
          <TooltipIconButton
            tooltip="More"
            className="data-[state=open]:bg-accent"
          >
            <MoreHorizontalIcon />
          </TooltipIconButton>
        </ActionBarMorePrimitive.Trigger>
        <ActionBarMorePrimitive.Content
          side="bottom"
          align="start"
          sideOffset={6}
          className="aui-action-bar-more-content bg-popover/95 text-popover-foreground data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=open]:animate-in data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=closed]:animate-out data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 z-50 min-w-[8rem] overflow-hidden rounded-xl border p-1.5 shadow-lg backdrop-blur-sm"
        >
          <ActionBarPrimitive.ExportMarkdown asChild>
            <ActionBarMorePrimitive.Item className="aui-action-bar-more-item hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-sm outline-none select-none">
              <DownloadIcon className="size-4" />
              Export as Markdown
            </ActionBarMorePrimitive.Item>
          </ActionBarPrimitive.ExportMarkdown>
        </ActionBarMorePrimitive.Content>
      </ActionBarMorePrimitive.Root>
    </ActionBarPrimitive.Root>
  );
};

// UserMessage renders human speech. The logged-in viewer's own messages
// (sender_id matches an identity of theirs) align right in the accent bubble;
// other humans align left with their name — a group chat with the assistant
// and other users on the left, "me" on the right.
const UserMessage: FC = () => {
  const meta = useMessageMeta();
  const createdAt = useAuiState(
    (s) => s.message.createdAt as Date | undefined,
  );
  // History messages carry an explicit isMe verdict; live-composed messages
  // and legacy paths without one default to "me" (the composer is the
  // viewer).
  const isMe = meta.isMe !== false;
  return (
    <MessagePrimitive.Root
      data-slot="aui_user-message-root"
      className={cn(
        "fade-in slide-in-from-bottom-1 animate-in grid auto-rows-auto content-start gap-y-2 px-2 duration-150",
        isMe
          ? "grid-cols-[minmax(72px,1fr)_auto] [&:where(>*)]:col-start-2"
          : "grid-cols-[auto_minmax(72px,1fr)] [&:where(>*)]:col-start-1",
      )}
      data-role="user"
    >
      <UserMessageAttachments />

      <div
        className={cn(
          "aui-user-message-content-wrapper relative min-w-0",
          isMe ? "col-start-2" : "col-start-1",
        )}
      >
        {/* Inside the bubble wrapper so it always stacks directly above the
            bubble — as a grid child it would land in the spacer column. */}
        {meta.senderName || meta.compressed || createdAt ? (
          <div
            data-slot="aui_user-message-header"
            className={cn(
              "mb-0.5 flex items-baseline gap-2 px-1",
              isMe ? "justify-end" : "justify-start",
            )}
          >
            {meta.senderName ? (
              <span className="text-muted-foreground text-xs font-medium">
                {meta.senderName}
              </span>
            ) : null}
            {meta.compressed ? <CompressedChip /> : null}
            <MessageTimestamp />
          </div>
        ) : null}
        {meta.media ? (
          <div className={cn("mb-1.5", !isMe && "[&>div]:items-start")}>
            <MediaAttachments media={meta.media} preview={meta.mediaPreview} />
          </div>
        ) : null}
        <div
          className={cn(
            "aui-user-message-content peer text-foreground rounded-xl px-4 py-2 wrap-break-word empty:hidden",
            isMe ? "bg-muted" : "bg-background border",
          )}
        >
          {/* NOTE: do not swap Text for MarkdownText here — the registry
              MarkdownText renders empty outside assistant messages. */}
          <MessagePrimitive.Parts />
        </div>
        {/* Edit action bar removed — editing history is not supported by the
            nagobot backend (messages are an append-only session log). */}
      </div>

      <BranchPicker
        data-slot="aui_user-branch-picker"
        className="col-span-full col-start-1 row-start-3 -me-1 justify-end"
      />
    </MessagePrimitive.Root>
  );
};

const EditComposer: FC = () => {
  return (
    <MessagePrimitive.Root
      data-slot="aui_edit-composer-wrapper"
      className="flex flex-col px-2"
    >
      <ComposerPrimitive.Root className="aui-edit-composer-root border-border/60 dark:border-muted-foreground/15 ms-auto flex w-full max-w-[85%] flex-col rounded-(--composer-radius) border bg-(--composer-bg) shadow-[0_4px_16px_-8px_rgba(0,0,0,0.08),0_1px_2px_rgba(0,0,0,0.04)] dark:shadow-none">
        <ComposerPrimitive.Input
          className="aui-edit-composer-input text-foreground min-h-14 w-full resize-none bg-transparent px-4 pt-3 pb-1 text-base outline-none"
          autoFocus
        />
        <div className="aui-edit-composer-footer mx-2.5 mb-2.5 flex items-center gap-1.5 self-end">
          <ComposerPrimitive.Cancel asChild>
            <Button
              variant="ghost"
              size="sm"
              className="h-8 rounded-full px-3.5"
            >
              Cancel
            </Button>
          </ComposerPrimitive.Cancel>
          <ComposerPrimitive.Send asChild>
            <Button size="sm" className="h-8 rounded-full px-3.5">
              Update
            </Button>
          </ComposerPrimitive.Send>
        </div>
      </ComposerPrimitive.Root>
    </MessagePrimitive.Root>
  );
};

const BranchPicker: FC<BranchPickerPrimitive.Root.Props> = ({
  className,
  ...rest
}) => {
  return (
    <BranchPickerPrimitive.Root
      hideWhenSingleBranch
      className={cn(
        "aui-branch-picker-root text-muted-foreground -ms-2 me-2 inline-flex items-center text-xs",
        className,
      )}
      {...rest}
    >
      <BranchPickerPrimitive.Previous asChild>
        <TooltipIconButton tooltip="Previous">
          <ChevronLeftIcon />
        </TooltipIconButton>
      </BranchPickerPrimitive.Previous>
      <span className="aui-branch-picker-state font-medium">
        <BranchPickerPrimitive.Number /> / <BranchPickerPrimitive.Count />
      </span>
      <BranchPickerPrimitive.Next asChild>
        <TooltipIconButton tooltip="Next">
          <ChevronRightIcon />
        </TooltipIconButton>
      </BranchPickerPrimitive.Next>
    </BranchPickerPrimitive.Root>
  );
};
