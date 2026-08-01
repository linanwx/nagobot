"use client";

import {
  ComposerAddAttachment,
  ComposerAttachments,
  UserMessageAttachments,
} from "@/components/assistant-ui/attachment";
import { ThreadFollowupSuggestions } from "@/components/assistant-ui/follow-up-suggestions";
import { MarkdownImage } from "@/components/assistant-ui/markdown-image";
import { ComposerQueueBar } from "@/components/assistant-ui/composer-queue";
import { MarkdownText } from "@/components/assistant-ui/markdown-text";
import { PinButton } from "@/components/assistant-ui/pin-message";
import {
  ComposerQuoteBar,
  ReplyQuoteButton,
} from "@/components/assistant-ui/quote-reply";
import { Reasoning } from "@/components/assistant-ui/reasoning";
import { ToolFallback } from "@/components/assistant-ui/tool-fallback";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
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
  ComposerPrimitive,
  ErrorPrimitive,
  groupPartByType,
  MessagePrimitive,
  SuggestionPrimitive,
  type TextMessagePartComponent,
  ThreadPrimitive,
  type ToolCallMessagePartComponent,
  useAuiState,
} from "@assistant-ui/react";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  BrainIcon,
  CheckIcon,
  ChevronDownIcon,
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
import { useTranslation } from "react-i18next";
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
  // History entries above the rendered window; > 0 shows the "load earlier"
  // control at the top of the thread.
  earlierCount?: number | undefined;
  onLoadEarlier?: (() => void) | undefined;
};

const EMPTY_COMPONENTS: ThreadComponents = {};

const ThreadComponentsContext =
  createContext<ThreadComponents>(EMPTY_COMPONENTS);

// Startup exposes a loading placeholder thread; treat it as a new chat so
// the composer mounts centered. Loads after startup keep the docked layout.
const isNewChatView = (s: AssistantState) =>
  s.thread.messages.length === 0 &&
  (!s.thread.isLoading || s.threads.isLoading);

export const Thread: FC<ThreadProps> = ({
  components = EMPTY_COMPONENTS,
  earlierCount = 0,
  onLoadEarlier,
}) => {
  const isEmpty = useAuiState(isNewChatView);

  return (
    <ThreadComponentsContext.Provider value={components}>
      <ThreadRoot
        isEmpty={isEmpty}
        earlierCount={earlierCount}
        onLoadEarlier={onLoadEarlier}
      />
    </ThreadComponentsContext.Provider>
  );
};

const ThreadRoot: FC<{
  isEmpty: boolean;
  earlierCount: number;
  onLoadEarlier?: (() => void) | undefined;
}> = ({ isEmpty, earlierCount, onLoadEarlier }) => {
  const { t } = useTranslation();
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

          {earlierCount > 0 && onLoadEarlier && (
            <div className="mb-4 flex justify-center">
              <button
                type="button"
                onClick={onLoadEarlier}
                className="text-muted-foreground hover:bg-muted hover:text-foreground rounded-full border px-3 py-1 text-xs"
              >
                {t("thread.loadEarlier", { count: earlierCount })}
              </button>
            </div>
          )}

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
              // Design padding ADDS to the safe area rather than max()-ing
              // with it. max() let a 34px home-indicator inset swallow the
              // 1rem entirely, so on notched phones the composer sat flush on
              // the safe-area boundary with no visual gap above the indicator
              // (measured on iOS 18.7). The inset is a floor to build on, not
              // a substitute for spacing. The md: variant needs it too — a
              // landscape phone is wide enough to hit that breakpoint.
              "aui-thread-viewport-footer bg-background flex flex-col gap-4 overflow-visible pb-[calc(1rem+var(--safe-bottom))] md:pb-[calc(1.5rem+var(--safe-bottom))]",
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
  if (isEditing) return <EditComposer />;
  if (role === "user") return <UserMessage />;
  return <AssistantMessageComponent />;
};


// MediaAttachments renders a user message's attachments from the protected
// /api/media route: images inline, audio as a player, anything else as a
// download link. The upfront AI description/transcription from the wake
// frontmatter is deliberately NOT rendered — it is model context, not chat,
// and long transcriptions drowned the conversation (raw dialog still has it).
const MediaAttachments: FC<{
  media: NonNullable<MessageMeta["media"]>;
}> = ({ media }) => (
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
  </div>
);

// Event bodies longer than this collapse by default. Text-length heuristic on
// purpose: it is stable before layout, so cards never resize after mount.
// (Message roots deliberately do NOT use content-visibility:auto — off-screen
// placeholder heights made scroll positions jump as real heights resolved.)
const eventCollapseChars = 300;
// Matches the collapsed max-h-16 clamp (~4 lines at 11px), so a body is only
// called collapsible when collapsing would actually hide something.
const eventCollapseLines = 4;

// Avatar tint per event source, so the event kind is scannable at a glance:
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
      // Not bg-muted: the bubble beside it is muted too, so the avatar needs
      // its own contrast to stay readable next to it.
      return "bg-foreground/10 text-muted-foreground";
  }
}

// Avatar glyph for a system event: the source's initial (H for heartbeat, C
// for cron) distinguishes event kinds the way a person's initial does for
// speakers.
function eventInitial(source: string | undefined): string {
  return (source ?? "system").trim().charAt(0).toUpperCase() || "S";
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
  img: (p) => <MarkdownImage {...p} />,
};

// Markdown for user bubbles. The registry MarkdownText is unusable here for the
// same reason as in event cards (it renders through the assistant message part
// context) and for a second one: its aui-md palette assumes a page background,
// so `code` and `blockquote` become unreadable inside the dark bg-primary
// bubble. Every color below is therefore derived from `currentColor`, which the
// bubble already sets correctly for both variants (light text on bg-primary for
// the viewer, dark text on bg-muted for other people).
const userMarkdownComponents: Components = {
  h1: (p) => <h3 className="mt-2 mb-1.5 font-semibold first:mt-0" {...p} />,
  h2: (p) => <h3 className="mt-2 mb-1.5 font-semibold first:mt-0" {...p} />,
  h3: (p) => <h4 className="mt-2 mb-1.5 font-semibold first:mt-0" {...p} />,
  p: (p) => <p className="mb-1.5 last:mb-0" {...p} />,
  ul: (p) => <ul className="mb-1.5 list-disc ps-4 last:mb-0" {...p} />,
  ol: (p) => <ol className="mb-1.5 list-decimal ps-4 last:mb-0" {...p} />,
  li: (p) => <li className="mb-0.5 last:mb-0" {...p} />,
  // The reply quote lands here: one line, first in the message.
  blockquote: (p) => (
    <blockquote
      className="border-current/40 mb-2 border-s-2 ps-2 text-[0.92em] opacity-75 last:mb-0"
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
    <code className="bg-current/15 rounded px-1 py-px font-mono text-[0.9em]" {...p} />
  ),
  pre: (p) => (
    <pre
      className="bg-current/15 mb-1.5 overflow-x-auto rounded p-2 text-[0.85em] last:mb-0 [&_code]:bg-transparent [&_code]:p-0"
      {...p}
    />
  ),
  hr: () => <hr className="border-current/30 my-2" />,
  table: (p) => (
    <div className="mb-1.5 overflow-x-auto last:mb-0">
      <table className="text-[0.9em]" {...p} />
    </div>
  ),
  th: (p) => (
    <th className="border-current/30 border px-1.5 py-0.5 text-start" {...p} />
  ),
  td: (p) => <td className="border-current/30 border px-1.5 py-0.5" {...p} />,
  img: (p) => <MarkdownImage {...p} />,
};

// UserMarkdownText renders a user message's text part as markdown. Users write
// markdown (tables pasted from elsewhere, code, and — since reply quotes ride
// in the text — a leading "> " line), so rendering it raw showed the syntax
// instead of the formatting.
const UserMarkdownText: TextMessagePartComponent = ({ text }) => (
  <ReactMarkdown remarkPlugins={[remarkGfm]} components={userMarkdownComponents}>
    {text}
  </ReactMarkdown>
);

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
  const { t } = useTranslation();
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
      ? t("thread.eventFrom", { caller: meta.caller })
      : null;
  return (
    <MessagePrimitive.Root
      data-slot="aui_event-message-root"
      className="-my-1 px-2"
      data-role="event"
    >
      {/* Same shape as another person's message — avatar circle beside a
          bubble — so system traffic reads as a speaker rather than a separate
          card format. Its palette stays fainter than human speech: the daemon
          is talking, but it should never compete with the conversation. */}
      <div className="flex items-start gap-3">
        <div
          data-slot="aui_event-message-avatar"
          title={meta.source}
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-full text-[10px] font-medium select-none",
            eventBadgeClass(meta.source),
          )}
        >
          {eventInitial(meta.source)}
        </div>
        <div className="min-w-0 flex-1">
          <div
            data-slot="aui_event-message-header"
            className="mb-0.5 flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-1"
          >
            {/* The speaker's name carries the event type: "System heartbeat". */}
            <span className="text-muted-foreground/70 text-xs font-medium">
              {meta.source
                ? `${t("thread.eventSystem")} ${meta.source}`
                : t("thread.eventSystem")}
            </span>
            {direction ? (
              <span className="text-muted-foreground/60 font-mono text-[10px] break-all">
                {direction}
              </span>
            ) : null}
            <MessageTimestamp className="ms-auto" />
          </div>
          <div
            className={cn(
              "text-muted-foreground/70 rounded-2xl px-3 py-1.5 text-[11px]",
              quiet ? "bg-muted/30 opacity-75" : "bg-muted/50",
            )}
          >
            {quote !== "" && (
              <div className="border-border/60 text-muted-foreground/50 mb-1 line-clamp-2 border-s-2 ps-2 whitespace-pre-wrap italic wrap-break-word">
                {quote}
              </div>
            )}
            <div
              className={cn(
                "wrap-break-word",
                !markdown && "whitespace-pre-wrap",
                // ~4 lines at 11px — long wake payloads stay subordinate to
                // the conversation until expanded.
                !quiet && collapsed && "max-h-16 overflow-hidden",
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
                className="text-muted-foreground/60 hover:text-foreground mt-1 text-[10px] font-medium"
              >
                {expanded ? t("common.showLess") : t("common.showMore")}
              </button>
            )}
          </div>
        </div>
      </div>
    </MessagePrimitive.Root>
  );
};

const ThreadScrollToBottom: FC = () => {
  const { t } = useTranslation();
  return (
    <ThreadPrimitive.ScrollToBottom asChild>
      <TooltipIconButton
        tooltip={t("thread.scrollToBottom")}
        variant="outline"
        className="aui-thread-scroll-to-bottom dark:border-border dark:bg-background dark:hover:bg-accent absolute -top-12 z-10 self-center rounded-full p-4 disabled:invisible"
      >
        <ArrowDownIcon />
      </TooltipIconButton>
    </ThreadPrimitive.ScrollToBottom>
  );
};

const ThreadWelcome: FC = () => {
  const { t } = useTranslation();
  return (
    <div className="aui-thread-welcome-root mb-6 flex flex-col items-center px-4 text-center">
      <h1 className="aui-thread-welcome-message-inner fade-in slide-in-from-bottom-1 animate-in fill-mode-both text-2xl font-semibold duration-200">
        {t("thread.welcome")}
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
  const { t } = useTranslation();
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
          <ComposerQuoteBar />
          <ComposerQueueBar />
          <ComposerPrimitive.Input
            placeholder={t("thread.sendPlaceholder")}
            className="aui-composer-input caret-primary placeholder:text-muted-foreground/80 max-h-32 min-h-10 w-full resize-none bg-transparent px-2.5 py-1 text-base outline-none"
            rows={1}
            autoFocus={!coarsePointer}
            enterKeyHint="send"
            aria-label={t("thread.messageInput")}
          />
          <ComposerAction />
        </div>
      </ComposerPrimitive.AttachmentDropzone>
    </ComposerPrimitive.Root>
  );
};

const ComposerAction: FC = () => {
  const { t } = useTranslation();
  return (
    /*
      Every button in this row is size-9 below the sm breakpoint and size-7 at
      and above it. 28px is a comfortable mouse target and a poor finger one —
      Apple's minimum is 44 and Material's is 48, so the phone rendering was
      barely half the recommended AREA. The library's own tokens cannot express
      this: the largest is icon-lg at 36px, and it is not responsive. Note the
      size="icon" prop below is inert either way, since cn()'s twMerge drops the
      variant's size-8 in favour of whatever the className sets.
    */
    <div className="aui-composer-action-wrapper relative flex items-center justify-between">
      <ComposerAddAttachment />
      <div className="flex items-center gap-1.5">
        <AuiIf condition={(s) => s.thread.capabilities.dictation}>
          <AuiIf condition={(s) => s.composer.dictation == null}>
            <ComposerPrimitive.Dictate asChild>
              <TooltipIconButton
                tooltip={t("thread.voiceInput")}
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-dictate size-9 rounded-full sm:size-7"
                aria-label={t("thread.startVoice")}
              >
                <MicIcon className="aui-composer-dictate-icon size-4.5 sm:size-4" />
              </TooltipIconButton>
            </ComposerPrimitive.Dictate>
          </AuiIf>
          <AuiIf condition={(s) => s.composer.dictation != null}>
            <ComposerPrimitive.StopDictation asChild>
              <TooltipIconButton
                tooltip={t("thread.stopDictation")}
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-stop-dictation text-destructive size-9 rounded-full sm:size-7"
                aria-label={t("thread.stopVoice")}
              >
                <SquareIcon className="aui-composer-stop-dictation-icon size-4 animate-pulse fill-current sm:size-3.5" />
              </TooltipIconButton>
            </ComposerPrimitive.StopDictation>
          </AuiIf>
        </AuiIf>
        {/*
          Send stays available during a run — the daemon accepts messages
          mid-turn and merges or injects them (thread/wake.go), so there is no
          reason to make the user wait. There is deliberately no stop button
          in its place: this runtime supplies no onCancel, and the daemon
          exposes no cancel path over the web channel, so the button that used
          to sit here rendered permanently disabled and did nothing.
        */}
        <ComposerPrimitive.Send asChild>
          <TooltipIconButton
            tooltip={t("thread.sendMessage")}
            side="bottom"
            type="button"
            variant="default"
            size="icon"
            className="aui-composer-send size-9 rounded-full sm:size-7"
            aria-label={t("thread.sendMessage")}
          >
            <ArrowUpIcon className="aui-composer-send-icon size-5 sm:size-4.5" />
          </TooltipIconButton>
        </ComposerPrimitive.Send>
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

// ChainRow is one line of the thinking card: a small circular bullet in the
// gutter, content in the rest. Tool calls bring their own trigger row, so this
// is used for reasoning only — it exists to keep the gutter aligned between
// the two.
const ChainRow: FC<PropsWithChildren<{ icon: React.ReactNode }>> = ({
  icon,
  children,
}) => (
  <div className="flex gap-3 px-4 py-1.5">
    <div className="bg-muted mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full">
      {icon}
    </div>
    {children}
  </div>
);

// ChainOfThoughtCard is the single disclosure that wraps a turn's whole
// reasoning + tool chain. It opens itself while the turn is running and
// collapses when the turn ends, and the first manual toggle takes over from
// then on — same auto/manual handover as ReasoningRoot, which this replaces
// at the group level.
//
// "Running" is derived from the MESSAGE, not from the group's own status.
// GroupedParts sets a group's status to its LAST contained part's status, and a
// tool-call part flips to "complete" the instant its result lands — so every
// tool result inside a still-running turn read as "the chain ended" and slammed
// the card shut, only for the next reasoning delta to reopen it. The message's
// own status is the thing that actually tracks the turn. The trailing-group test
// keeps the behaviour that motivated the original status read: once the model
// starts writing its reply, parts appear AFTER this group and the chain stops
// being the live edge, so it collapses even though the message is still running.
const ChainOfThoughtCard: FC<
  PropsWithChildren<{ indices: readonly number[] }>
> = ({ indices, children }) => {
  const { t } = useTranslation();
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  // Two primitive subscriptions rather than one closure over `indices`: the
  // selector then never changes identity as the group grows.
  const messageRunning = useAuiState(
    (s) => s.message.status?.type === "running",
  );
  const partCount = useAuiState((s) => s.message.parts.length);
  const running = messageRunning && indices.at(-1) === partCount - 1;
  const open = userOpen ?? running;

  return (
    <Collapsible
      open={open}
      onOpenChange={setUserOpen}
      data-slot="aui_chain-of-thought"
      className="border-border/80 bg-background/90 my-2 overflow-hidden rounded-xl border shadow-sm"
    >
      <CollapsibleTrigger className="hover:bg-muted/50 group/cot-trigger flex w-full cursor-pointer items-center gap-2 px-4 py-2.5 text-sm font-medium transition-colors">
        <ChevronDownIcon
          className={cn(
            "size-4 shrink-0 transition-transform duration-200",
            !open && "-rotate-90",
          )}
        />
        <span className={cn(running && "shimmer")}>{t("thread.thinking")}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t py-1.5">
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
};

const AssistantMessage: FC = () => {
  const { t } = useTranslation();
  const {
    ToolFallback: ToolFallbackComponent = ToolFallback,
    ToolGroup,
    ReasoningGroup,
  } = useContext(ThreadComponentsContext);
  const meta = useMessageMeta();
  const createdAt = useAuiState(
    (s) => s.message.createdAt as Date | undefined,
  );
  // The action bar only offers copy and export-markdown, both of which act on
  // the message's TEXT. A turn that produced only a chain of thought (reasoning
  // and tool calls, no reply — e.g. one that ended via dispatch) has nothing for
  // them to act on, so the whole footer is dropped rather than reserving its
  // 30px of blank space under the Thinking card.
  const hasText = useAuiState((s) =>
    s.message.content.some(
      (part) => part.type === "text" && part.text.trim() !== "",
    ),
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
      {meta.caller || createdAt ? (
        <div
          data-slot="aui_assistant-message-header"
          className="flex items-baseline gap-2 px-2 pb-0.5"
        >
          {meta.caller ? (
            <span className="text-muted-foreground/70 font-mono text-[10px] break-all">
              {t("thread.via", { caller: meta.caller })}
            </span>
          ) : null}
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
              // ONE card per turn, with a single "Thinking" disclosure. The
              // reasoning and tool groups nested under it render nothing of
              // their own — their children become flat rows in this card's
              // body, so a turn reads as one chain rather than a stack of
              // separately-collapsible "Reasoning" and "N tool calls" boxes.
              case "group-chainOfThought":
                return (
                  <ChainOfThoughtCard indices={part.indices}>
                    {children}
                  </ChainOfThoughtCard>
                );
              case "group-tool":
                if (ToolGroup) {
                  return <ToolGroup group={part}>{children}</ToolGroup>;
                }
                return <>{children}</>;
              case "group-reasoning": {
                if (ReasoningGroup) {
                  return (
                    <ReasoningGroup group={part}>{children}</ReasoningGroup>
                  );
                }
                return <>{children}</>;
              }
              case "text":
                return <MarkdownText />;
              // A reasoning part is one row of the chain: a brain bullet plus
              // the thought itself, muted and italic. It never carries its own
              // disclosure — the card's "Thinking" toggle governs the whole
              // chain.
              case "reasoning":
                return (
                  <ChainRow
                    icon={
                      <BrainIcon className="text-muted-foreground size-3" />
                    }
                  >
                    <div className="text-muted-foreground min-w-0 flex-1 text-sm leading-relaxed italic">
                      <Reasoning {...part} />
                    </div>
                  </ChainRow>
                );
              // Tool calls stay individually expandable (ToolFallback is its
              // own collapsible row) — only their group wrapper is gone.
              case "tool-call":
                return (
                  <div className="px-4 py-0.5">
                    {part.toolUI ?? <ToolFallbackComponent {...part} />}
                  </div>
                );
              case "data":
                return part.dataRendererUI;
              case "indicator":
                return (
                  <span
                    data-slot="aui_assistant-message-indicator"
                    className="animate-pulse font-sans"
                    aria-label={t("thread.working")}
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

      {hasText ? (
        <div
          data-slot="aui_assistant-message-footer"
          className={cn("ms-2 flex items-center", ACTION_BAR_HEIGHT)}
        >
          <AssistantActionBar />
        </div>
      ) : null}
    </MessagePrimitive.Root>
  );
};

const AssistantActionBar: FC = () => {
  const { t } = useTranslation();
  return (
    <ActionBarPrimitive.Root
      hideWhenRunning
      autohide="not-last"
      className="aui-assistant-action-bar-root text-muted-foreground animate-in fade-in col-start-3 row-start-2 -ms-1 flex gap-1 duration-200"
    >
      <ActionBarPrimitive.Copy asChild>
        <TooltipIconButton tooltip={t("common.copy")}>
          <AuiIf condition={(s) => s.message.isCopied}>
            <CheckIcon className="animate-in zoom-in-50 fade-in duration-200 ease-out" />
          </AuiIf>
          <AuiIf condition={(s) => !s.message.isCopied}>
            <CopyIcon className="animate-in zoom-in-75 fade-in duration-150" />
          </AuiIf>
        </TooltipIconButton>
      </ActionBarPrimitive.Copy>
      <ReplyQuoteButton />
      <PinButton />
      {/* Reload removed — regenerating a reply is not supported by the
          nagobot backend (the session log is append-only). */}
      <ActionBarMorePrimitive.Root>
        <ActionBarMorePrimitive.Trigger asChild>
          <TooltipIconButton
            tooltip={t("common.more")}
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
              {t("thread.exportMarkdown")}
            </ActionBarMorePrimitive.Item>
          </ActionBarPrimitive.ExportMarkdown>
        </ActionBarMorePrimitive.Content>
      </ActionBarMorePrimitive.Root>
    </ActionBarPrimitive.Root>
  );
};

// Leading glyph for another person's avatar circle. Sender names are channel
// handles ("web-user", "Nansen"), so the first character is the only stable
// initial available.
const senderInitial = (name: string): string =>
  name.trim().charAt(0).toUpperCase() || "?";

// UserMessage renders human speech, following assistant-ui's native two-party
// bubble pattern: the logged-in viewer's own messages align right in the
// primary bubble; other humans align left behind an avatar circle in the muted
// bubble — a group chat with the assistant and other users on the left, "me"
// on the right.
const UserMessage: FC = () => {
  const meta = useMessageMeta();
  const createdAt = useAuiState(
    (s) => s.message.createdAt as Date | undefined,
  );
  // History messages carry an explicit isMe verdict; live-composed messages
  // and legacy paths without one default to "me" (the composer is the
  // viewer).
  const isMe = meta.isMe !== false;
  // An image sent with no caption still carries an empty text part, so Parts
  // renders a child node and the bubble's `empty:hidden` never matches —
  // leaving a blank pill under the picture. Gate on real content instead.
  // Non-text parts keep the bubble: only an all-empty-text message drops it.
  const hasBubbleContent = useAuiState((s) =>
    s.message.content.some(
      (part) => part.type !== "text" || part.text.trim() !== "",
    ),
  );
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
        {/* Another person's turn gets the native avatar + bubble row; the
            viewer's own turn needs no avatar (the right edge identifies it). */}
        <div className={cn(!isMe && "flex items-start gap-3")}>
          {!isMe && meta.senderName ? (
            <div
              data-slot="aui_user-message-avatar"
              title={meta.senderName}
              className="bg-primary/10 text-primary flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-medium select-none"
            >
              {senderInitial(meta.senderName)}
            </div>
          ) : null}
          <div className="min-w-0 flex-1">
            {/* Header lives in the bubble column so the name shares the
                bubble's left edge; as a grid child it would land in the
                spacer column instead. */}
            {meta.senderName || createdAt ? (
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
                <MessageTimestamp />
              </div>
            ) : null}
            {meta.media ? (
              <div className={cn("mb-1.5", !isMe && "[&>div]:items-start")}>
                <MediaAttachments media={meta.media} />
              </div>
            ) : null}
            {hasBubbleContent ? (
              <div
                className={cn(
                  "aui-user-message-content peer rounded-2xl px-4 py-2.5 text-sm wrap-break-word empty:hidden",
                  isMe
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-foreground",
                )}
              >
                <MessagePrimitive.Parts
                  components={{ Text: UserMarkdownText }}
                />
              </div>
            ) : null}
          </div>
        </div>
        {/* Edit action bar removed — editing history is not supported by the
            nagobot backend (messages are an append-only session log). */}
      </div>
    </MessagePrimitive.Root>
  );
};

const EditComposer: FC = () => {
  const { t } = useTranslation();
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
              {t("common.cancel")}
            </Button>
          </ComposerPrimitive.Cancel>
          <ComposerPrimitive.Send asChild>
            <Button size="sm" className="h-8 rounded-full px-3.5">
              {t("thread.update")}
            </Button>
          </ComposerPrimitive.Send>
        </div>
      </ComposerPrimitive.Root>
    </MessagePrimitive.Root>
  );
};

// NOTE: there is deliberately no BranchPicker in this file, and it must not be
// reintroduced. A "branch" is a message with siblings, and nagobot never
// produces one on purpose — session.jsonl is an append-only log with no edit or
// regenerate path, and the runtime is never given `setMessages`, so
// `switchToBranch` throws outright ("Runtime does not support switching
// branches"). The picker was dead UI whose arrows raised on click.
//
// It still showed up — a stray "2 / 2" under a user bubble — because siblings
// arise by accident. A message optimistically appended with a local id
// (`localID("user")` in use-nagobot-chat.ts) is re-fetched by `resync()` under
// its persisted id, and assistant-ui parents every message on the PREVIOUS
// array entry: the same message thus enters the repository twice, under two
// ids, as two children of one parent. Nothing reaps the ghost — the
// messages-array path of ExternalStoreThreadRuntimeCore never prunes ids that
// left the array (only the messageRepository path does), and `resetHead`
// deletes the new head's descendants, never its siblings. Harmless as long as
// nothing renders branch state.
