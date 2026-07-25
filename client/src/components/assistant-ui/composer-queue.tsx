"use client";

import { ComposerPrimitive, QueueItemPrimitive } from "@assistant-ui/react";
import { ClockIcon } from "lucide-react";
import type { FC } from "react";
import { useTranslation } from "react-i18next";

/**
 * ComposerQueueBar shows messages already handed to the daemon that have not
 * yet been given a place in the conversation.
 *
 * The daemon owns placement, and the client cannot know at send time what it
 * will decide: a message arriving mid-turn may start its own next turn, be
 * merged with a sibling into one wake, or be injected into the middle of the
 * running turn at a tool-iteration boundary (thread/wake.go's tryMerge and
 * injectFn). Rendering the bubble optimistically at the tail would therefore
 * put it where the next history read contradicts it. So a sent message waits
 * here — outside the transcript — until its position is known, and only then
 * becomes a real bubble.
 *
 * Deliberately no steer and no remove control: the message is already on the
 * wire and the daemon has no cancel path, so a chip is a statement about where
 * a message is, never a control over it.
 */
export const ComposerQueueBar: FC = () => {
  const { t } = useTranslation();
  return (
    <ComposerPrimitive.Queue>
      {() => (
        <div
          className="border-border/60 text-muted-foreground bg-muted/40 flex items-start gap-2 rounded-lg border-s-2 py-1.5 pe-2 ps-2.5 text-xs"
          role="status"
          aria-label={t("thread.queuedMessage")}
          title={t("thread.queuedMessage")}
        >
          <ClockIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <QueueItemPrimitive.Text className="line-clamp-2 min-w-0 flex-1 whitespace-pre-wrap" />
        </div>
      )}
    </ComposerPrimitive.Queue>
  );
};
