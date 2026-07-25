"use client";

import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { ComposerPrimitive, useAui, useAuiState } from "@assistant-ui/react";
import { AlertCircleIcon, QuoteIcon, XIcon } from "lucide-react";
import {
  createContext,
  useContext,
  useState,
  type FC,
  type PropsWithChildren,
} from "react";
import { useTranslation } from "react-i18next";

/**
 * Quote-reply: the Reply button on a message, and the quote preview in the
 * composer.
 *
 * The quote is ONE line of markdown quote text — "> …", marker included —
 * produced whole by the generator. Nothing here inspects or builds quote
 * syntax, and nothing here knows what the generator is (today an LLM turn on
 * the daemon). That is the seam: swap the generator and this file is untouched.
 *
 * The line is not attached to the quoted message. It goes into the composer,
 * gets prepended to the outgoing text on send, and from then on it is just the
 * first line of a normal markdown message — which is why a reloaded history
 * renders it identically, with no quote-aware code on the display side.
 */

/** Turns the text being replied to into one line of markdown quote. */
export type QuoteGenerator = (text: string) => Promise<string>;

const QuoteGeneratorContext = createContext<QuoteGenerator | null>(null);

/**
 * Supplies the quote generator to the thread below it. Without this provider
 * the Reply button does not render — a thread with no way to make a quote
 * should not offer to.
 */
export const QuoteReplyProvider: FC<
  PropsWithChildren<{ generate: QuoteGenerator }>
> = ({ generate, children }) => (
  <QuoteGeneratorContext.Provider value={generate}>
    {children}
  </QuoteGeneratorContext.Provider>
);

/**
 * ReplyQuoteButton asks for a quote of the message it sits on and hands it to
 * the composer. Runs through three visible states: idle, loading (the request
 * is in flight), and error — errors are shown in the tooltip and logged, never
 * swallowed, because there is no fallback quote to fall back to.
 */
export const ReplyQuoteButton: FC = () => {
  const { t } = useTranslation();
  const generate = useContext(QuoteGeneratorContext);
  const aui = useAui();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  const messageId = useAuiState((s) => s.message.id);
  // Text parts only: attachments and tool calls are not quotable speech.
  const text = useAuiState((s) =>
    s.message.content
      .filter((part) => part.type === "text")
      .map((part) => part.text)
      .join("\n\n")
      .trim(),
  );

  if (!generate || !text) return null;

  const onClick = async () => {
    if (pending) return;
    setPending(true);
    setError("");
    try {
      const quote = await generate(text);
      aui.thread().composer().setQuote({ text: quote, messageId });
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      console.error("quote failed", err);
      setError(detail);
    } finally {
      setPending(false);
    }
  };

  return (
    <TooltipIconButton
      tooltip={
        error
          ? t("thread.replyFailed", { error })
          : pending
            ? t("thread.replyPending")
            : t("thread.reply")
      }
      onClick={onClick}
      disabled={pending}
      aria-busy={pending}
    >
      {pending ? (
        <span
          className="border-current/30 border-t-current size-3.5 animate-spin rounded-full border-2"
          role="status"
        />
      ) : error ? (
        <AlertCircleIcon className="text-destructive" />
      ) : (
        <QuoteIcon />
      )}
    </TooltipIconButton>
  );
};

/**
 * ComposerQuoteBar previews the pending quote above the input, with an ❌ to
 * drop it. Renders only while a quote is set — that gate, the text, and the
 * dismiss action are all assistant-ui primitives.
 */
export const ComposerQuoteBar: FC = () => {
  const { t } = useTranslation();
  return (
    <ComposerPrimitive.Quote className="border-border/60 text-muted-foreground bg-muted/40 flex items-start gap-2 rounded-lg border-s-2 py-1.5 pe-1 ps-2.5 text-xs">
      <ComposerPrimitive.QuoteText className="line-clamp-2 min-w-0 flex-1 italic" />
      <ComposerPrimitive.QuoteDismiss asChild>
        <TooltipIconButton
          tooltip={t("thread.removeQuote")}
          side="top"
          type="button"
          className="-mt-0.5 size-5 shrink-0"
        >
          <XIcon className="size-3.5" />
        </TooltipIconButton>
      </ComposerPrimitive.QuoteDismiss>
    </ComposerPrimitive.Quote>
  );
};
