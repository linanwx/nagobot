"use client";

import { ComposerPrimitive, QueueItemPrimitive } from "@assistant-ui/react";
import { AlertCircleIcon, ClockIcon, Loader2Icon } from "lucide-react";
import type { FC } from "react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

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
 * Deliberately no steer and no remove control: a sent message is already on the
 * wire and the daemon has no cancel path, so a chip is a statement about where
 * something is, never a control over it.
 */
export const ComposerQueueBar: FC = () => {
  const { t } = useTranslation();
  return (
    <ComposerPrimitive.Queue>
      {({ queueItem }) => {
        const isUpload = queueItem.id.startsWith("upload:");
        const isError = queueItem.id.startsWith("upload-error:");
        const label = isError
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
          </div>
        );
      }}
    </ComposerPrimitive.Queue>
  );
};
