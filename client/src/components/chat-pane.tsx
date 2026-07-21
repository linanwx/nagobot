import { AssistantRuntimeProvider } from "@assistant-ui/react";
import { ArrowUpLeft, ChevronLeft, MoreHorizontal } from "lucide-react";
import { Thread } from "@/components/assistant-ui/thread";
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
import type { SessionEntry } from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

const statusLabel = {
  connecting: "connecting…",
  open: "online",
  closed: "reconnecting…",
  replaced: "inactive",
} as const;

// The child-session menu is capped: cli alone has hundreds of thread children.
const childMenuLimit = 50;

export function ChatPane({
  sessionKey,
  childSessions,
  parentSession,
  onOpenSession,
  onBack,
  hiddenOnMobile,
}: {
  sessionKey: string;
  childSessions: SessionEntry[];
  parentSession: string | null;
  onOpenSession: (key: string) => void;
  // Returns to the session list on mobile (list and chat are two views there).
  onBack: () => void;
  hiddenOnMobile?: boolean;
}) {
  const { runtime, status, historyError, historyLoading, takeOver } =
    useNagobotChat(sessionKey);

  const visibleChildren = childSessions.slice(0, childMenuLimit);

  return (
    <div
      className={cn(
        "h-full min-w-0 flex-1 flex-col md:flex",
        hiddenOnMobile ? "hidden" : "flex",
      )}
    >
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-2 md:px-4">
        <Button
          variant="ghost"
          size="icon"
          className="size-7 md:hidden"
          onClick={onBack}
        >
          <ChevronLeft className="size-4" />
          <span className="sr-only">Back to session list</span>
        </Button>
        <span className="truncate text-sm font-medium">{sessionKey}</span>
        <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
          <span
            className={cn(
              "size-2 rounded-full",
              status === "open" ? "bg-emerald-500" : "bg-amber-500",
            )}
          />
          {statusLabel[status]}
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7">
              <MoreHorizontal className="size-4" />
              <span className="sr-only">Session menu</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            {parentSession && (
              <>
                <DropdownMenuItem onSelect={() => onOpenSession(parentSession)}>
                  <ArrowUpLeft className="size-4" />
                  Back to {parentSession}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
              </>
            )}
            <DropdownMenuLabel>
              Child sessions
              {childSessions.length > 0 && ` (${childSessions.length})`}
            </DropdownMenuLabel>
            {visibleChildren.length === 0 ? (
              <DropdownMenuItem disabled>No child sessions</DropdownMenuItem>
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
                    …and {childSessions.length - childMenuLimit} more
                  </DropdownMenuItem>
                )}
              </div>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </header>
      {historyError && (
        <p className="border-b bg-destructive/10 px-4 py-1 text-xs text-destructive">
          Failed to load history: {historyError}
        </p>
      )}
      <div className="min-h-0 flex-1">
        {status === "replaced" ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
            <p className="text-sm font-medium">
              This session is now active in another window
            </p>
            <p className="text-muted-foreground max-w-sm text-xs">
              Only one page can be connected to a session at a time. Messages
              are being delivered to the other window.
            </p>
            <Button size="sm" onClick={takeOver}>
              Use here instead
            </Button>
          </div>
        ) : historyLoading ? (
          <div className="flex h-full items-center justify-center">
            <div
              className="border-muted-foreground/30 border-t-foreground size-6 animate-spin rounded-full border-2"
              role="status"
              aria-label="Loading history"
            />
          </div>
        ) : (
          <AssistantRuntimeProvider runtime={runtime}>
            <Thread />
          </AssistantRuntimeProvider>
        )}
      </div>
    </div>
  );
}
