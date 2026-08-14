"use client";

import { ComposerPrimitive, QueueItemPrimitive } from "@assistant-ui/react";
import {
  AlertCircleIcon,
  ClockIcon,
  Loader2Icon,
  RotateCwIcon,
  XIcon,
} from "lucide-react";
import {
  createContext,
  useContext,
  useMemo,
  type FC,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";

import { failedChipPrefix } from "@/hooks/use-nagobot-chat";
import { cn } from "@/lib/utils";

// What the composer needs to know about whether words can leave this page:
// whether the link is up (the send button reads it) and how to put a failed
// message back on the wire (the chip's retry reads it). Both consumers live in
// the composer, so they share one provider, and it arrives the same way the
// quote and pin backends do — as plain values from ChatPane, leaving these
// components unaware of sockets and message ids.
//
// `connected` is deliberately supplied rather than read off the runtime:
// assistant-ui exposes the flag as thread.isSendDisabled on the ADAPTER but not
// on ThreadState, and composer.canSend cannot stand in for it because it is
// equally false for an empty composer — which must not look like an error.
type ComposerDelivery = {
  connected: boolean;
  retry: (id: string) => void;
};

const ComposerDeliveryContext = createContext<ComposerDelivery>({
  connected: true,
  retry: () => {},
});

export const useComposerDelivery = () => useContext(ComposerDeliveryContext);

export const ComposerDeliveryProvider: FC<
  ComposerDelivery & { children: ReactNode }
> = ({ connected, retry, children }) => {
  const value = useMemo(() => ({ connected, retry }), [connected, retry]);
  return (
    <ComposerDeliveryContext.Provider value={value}>
      {children}
    </ComposerDeliveryContext.Provider>
  );
};

/**
 * ComposerQueueBar shows work already handed over that has not yet landed in the
 * conversation. Two kinds share the bar, told apart by the item's id prefix
 * (`upload:`, set in use-nagobot-chat.ts):
 *
 * A **sent message** waits because the daemon owns placement, and the client
 * cannot know at send time what it will decide: a message arriving mid-turn may
 * start its own next turn, be merged with a sibling into one wake, or be
 * injected into the middle of the running turn at a tool-iteration boundary
 * (thread/wake.go's tryMerge and injectFn). Rendering the bubble optimistically
 * at the tail would therefore put it where the next history read contradicts it.
 * So it waits here — outside the transcript — until its position is known, and
 * only then becomes a real bubble.
 *
 * An **upload** waits because its bytes are still going up. The composer clears
 * itself the instant send is pressed and the message chip is not minted until
 * every attachment has resolved, so without this the screen would be entirely
 * empty for the length of the upload. Its text carries the file's own name and a
 * percentage, or the failure if it stopped.
 *
 * An **undelivered message** (`failed:`) is one that never reached the daemon —
 * either the socket refused it or no ack came back in time. It is the only chip
 * with controls, and it has them because it is the only one holding something
 * the user would otherwise lose: its own text. Retry puts those words back on
 * the wire; × gives up on them. A sent-and-acknowledged message gets neither,
 * since the daemon already has it and this channel has no cancel path — a chip
 * is a statement about where something is, never a control over it.
 */
export const ComposerQueueBar: FC = () => {
  const { t } = useTranslation();
  const { retry } = useComposerDelivery();
  return (
    <ComposerPrimitive.Queue>
      {({ queueItem }) => {
        const isUpload = queueItem.id.startsWith("upload:");
        const isUploadError = queueItem.id.startsWith("upload-error:");
        const isFailed = queueItem.id.startsWith(failedChipPrefix);
        const isError = isUploadError || isFailed;
        const label = isFailed
          ? t("thread.undeliveredMessage")
          : isUploadError
            ? t("attachment.uploadFailed")
            : isUpload
              ? t("thread.uploadingMessage")
              : t("thread.queuedMessage");
        return (
          <div
            className={cn(
              "flex items-start gap-2 rounded-lg border-s-2 py-1.5 pe-2 ps-2.5 text-xs",
              isError
                ? "border-destructive/60 text-destructive bg-destructive/5"
                : "border-border/60 text-muted-foreground bg-muted/40",
            )}
            role="status"
            aria-label={label}
            title={label}
          >
            {isError ? (
              <AlertCircleIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            ) : isUpload ? (
              <Loader2Icon
                className="mt-0.5 size-3.5 shrink-0 animate-spin"
                aria-hidden
              />
            ) : (
              <ClockIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            )}
            <QueueItemPrimitive.Text className="line-clamp-2 min-w-0 flex-1 whitespace-pre-wrap" />
            {isFailed && (
              <div className="flex shrink-0 items-center gap-0.5">
                <button
                  type="button"
                  onClick={() =>
                    retry(queueItem.id.slice(failedChipPrefix.length))
                  }
                  className="hover:bg-destructive/10 rounded p-1"
                  aria-label={t("thread.retrySend")}
                  title={t("thread.retrySend")}
                >
                  <RotateCwIcon className="size-3.5" aria-hidden />
                </button>
                <QueueItemPrimitive.Remove
                  className="hover:bg-destructive/10 rounded p-1"
                  aria-label={t("thread.discardMessage")}
                  title={t("thread.discardMessage")}
                >
                  <XIcon className="size-3.5" aria-hidden />
                </QueueItemPrimitive.Remove>
              </div>
            )}
          </div>
        );
      }}
    </ComposerPrimitive.Queue>
  );
};
