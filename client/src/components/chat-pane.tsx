import { AssistantRuntimeProvider } from "@assistant-ui/react";
import { ArrowUpLeft, FileJson, Menu, MoreHorizontal, Pin } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { PinProvider } from "@/components/assistant-ui/pin-message";
import { QuoteReplyProvider } from "@/components/assistant-ui/quote-reply";
import { Thread } from "@/components/assistant-ui/thread";
import { PinsDialog } from "@/components/pins-dialog";
import { SessionRawDialog } from "@/components/session-raw-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useNagobotChat } from "@/hooks/use-nagobot-chat";
import { createPin, fetchQuote, type SessionEntry } from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

// Blank welcome slot: shown instead of "How can I help you today?" while the
// runtime's internal store is still ingesting a session that HAS history —
// that sync runs in a post-mount effect, so the empty-thread welcome would
// flash for a frame or two before the messages appear.
const NullWelcome = () => null;

const statusLabelKey = {
  connecting: "chat.connecting",
  open: "chat.online",
  closed: "chat.reconnecting",
  replaced: "chat.inactive",
} as const;

// The child-session menu is capped: cli alone has hundreds of thread children.
const childMenuLimit = 50;

export function ChatPane({
  sessionKey,
  summary,
  childSessions,
  parentSession,
  onOpenSession,
  onOpenSidebar,
  onFirstSend,
}: {
  sessionKey: string;
  // The session's summary, if it has one — shown as the header title in place
  // of the opaque session id (which is kept as the hover tooltip / fallback).
  summary?: string;
  childSessions: SessionEntry[];
  parentSession: string | null;
  onOpenSession: (key: string) => void;
  // Opens the session-list drawer on mobile; desktop has the list permanently.
  onOpenSidebar: () => void;
  // Fired once, after this pane's first successful send, so the sidebar can
  // surface a session that until now existed only in this browser.
  onFirstSend?: (key: string) => void;
}) {
  const { t } = useTranslation();
  const {
    runtime,
    status,
    historyError,
    historyLoading,
    takeOver,
    messageCount,
    earlierCount,
    loadEarlier,
  } = useNagobotChat(sessionKey, onFirstSend);
  const hasMessages = messageCount > 0;
  const threadComponents = useMemo(
    () => (hasMessages ? { Welcome: NullWelcome } : undefined),
    [hasMessages],
  );
  const [rawOpen, setRawOpen] = useState(false);
  const [pinsOpen, setPinsOpen] = useState(false);
  // This pane is the only place that knows both the session and the endpoint;
  // the reply and pin components below take a plain function of the message
  // text and stay unaware of both, so swapping either backend never reaches
  // into them.
  const generateQuote = useCallback(
    (text: string) => fetchQuote(sessionKey, text),
    [sessionKey],
  );
  const filePin = useCallback(
    (text: string) => createPin(sessionKey, text),
    [sessionKey],
  );

  const visibleChildren = childSessions.slice(0, childMenuLimit);

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-2 md:px-4">
        <Button
          variant="ghost"
          size="icon"
          className="size-7 md:hidden"
          onClick={onOpenSidebar}
        >
          <Menu className="size-4" />
          <span className="sr-only">{t("chat.openSessionList")}</span>
        </Button>
        <span
          className="min-w-0 flex-1 truncate text-sm font-medium"
          title={sessionKey}
        >
          {summary || sessionKey}
        </span>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span
            className={cn(
              "size-2 rounded-full",
              status === "open" ? "bg-emerald-500" : "bg-amber-500",
            )}
          />
          {t(statusLabelKey[status])}
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7">
              <MoreHorizontal className="size-4" />
              <span className="sr-only">{t("chat.sessionMenu")}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            <DropdownMenuItem onSelect={() => setPinsOpen(true)}>
              <Pin className="size-4" />
              {t("pins.title")}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setRawOpen(true)}>
              <FileJson className="size-4" />
              {t("chat.rawSessionData")}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {parentSession && (
              <>
                <DropdownMenuItem onSelect={() => onOpenSession(parentSession)}>
                  <ArrowUpLeft className="size-4" />
                  {t("chat.backTo", { key: parentSession })}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
              </>
            )}
            <DropdownMenuLabel>
              {t("chat.childSessions")}
              {childSessions.length > 0 && ` (${childSessions.length})`}
            </DropdownMenuLabel>
            {visibleChildren.length === 0 ? (
              <DropdownMenuItem disabled>
                {t("chat.noChildSessions")}
              </DropdownMenuItem>
            ) : (
              <div className="max-h-80 overflow-y-auto">
                {visibleChildren.map((c) => (
                  <DropdownMenuItem
                    key={c.key}
                    onSelect={() => onOpenSession(c.key)}
                  >
                    <span className="min-w-0 flex-1 truncate">
                      {c.key.slice(sessionKey.length + 1)}
                    </span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      {relativeTime(c.updated_at)}
                    </span>
                  </DropdownMenuItem>
                ))}
                {childSessions.length > childMenuLimit && (
                  <DropdownMenuItem disabled>
                    {t("chat.andMore", {
                      count: childSessions.length - childMenuLimit,
                    })}
                  </DropdownMenuItem>
                )}
              </div>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
        <SessionRawDialog
          sessionKey={sessionKey}
          open={rawOpen}
          onOpenChange={setRawOpen}
        />
        <PinsDialog
          sessionKey={sessionKey}
          open={pinsOpen}
          onOpenChange={setPinsOpen}
        />
      </header>
      {historyError && (
        <p className="border-b bg-destructive/10 px-4 py-1 text-xs text-destructive">
          {t("chat.historyFailed", { error: historyError })}
        </p>
      )}
      <div className="min-h-0 flex-1">
        {status === "replaced" ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
            <p className="text-sm font-medium">{t("chat.replacedTitle")}</p>
            <p className="text-muted-foreground max-w-sm text-xs">
              {t("chat.replacedBody")}
            </p>
            <Button size="sm" onClick={takeOver}>
              {t("chat.useHere")}
            </Button>
          </div>
        ) : historyLoading ? (
          <div className="flex h-full items-center justify-center">
            <div
              className="border-muted-foreground/30 border-t-foreground size-6 animate-spin rounded-full border-2"
              role="status"
              aria-label={t("chat.loadingHistory")}
            />
          </div>
        ) : (
          <AssistantRuntimeProvider runtime={runtime}>
            <QuoteReplyProvider generate={generateQuote}>
              <PinProvider file={filePin}>
                <Thread
                  components={threadComponents}
                  earlierCount={earlierCount}
                  onLoadEarlier={loadEarlier}
                />
              </PinProvider>
            </QuoteReplyProvider>
          </AssistantRuntimeProvider>
        )}
      </div>
    </div>
  );
}
