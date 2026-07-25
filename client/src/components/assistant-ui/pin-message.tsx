"use client";

import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { useAuiState } from "@assistant-ui/react";
import { AlertCircleIcon, CheckIcon, PinIcon } from "lucide-react";
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type FC,
  type PropsWithChildren,
} from "react";
import { useTranslation } from "react-i18next";

/**
 * Pin: the button that files a message into the session's pins collection.
 *
 * It is a shortcut, not an editor — one click hands the message text to the
 * daemon and that is the user's entire involvement. Everything downstream
 * (whether this becomes a new note or is merged into an existing one, what it
 * is titled) is the pin agent's judgement, so nothing here inspects or shapes
 * the text, and this file never learns what a pin file looks like. That is the
 * same seam quote-reply keeps: swap the filer and this component is untouched.
 *
 * The button acknowledges the REQUEST, not the file. The write is an agentic
 * turn that runs afterwards, so success here means "queued" — the pins panel is
 * where the result eventually shows up.
 */

/** Files the given text into the session's pins. Resolves once queued. */
export type PinFiler = (text: string) => Promise<void>;

const PinFilerContext = createContext<PinFiler | null>(null);

/**
 * Supplies the pin filer to the thread below it. Without this provider the Pin
 * button does not render — a thread with no way to file a pin should not offer
 * to.
 */
export const PinProvider: FC<PropsWithChildren<{ file: PinFiler }>> = ({
  file,
  children,
}) => <PinFilerContext.Provider value={file}>{children}</PinFilerContext.Provider>;

// How long the green acknowledgement holds. It doubles as the anti-spam window:
// the button stays disabled for its duration, so a user who doesn't see an
// immediate file appear can't queue the same message five times.
const pinAckMs = 2500;

export const PinButton: FC = () => {
  const { t } = useTranslation();
  const file = useContext(PinFilerContext);
  const [state, setState] = useState<"idle" | "pending" | "done">("idle");
  const [error, setError] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Text parts only, matching the quote button: attachments and tool calls are
  // not pinnable content.
  const text = useAuiState((s) =>
    s.message.content
      .filter((part) => part.type === "text")
      .map((part) => part.text)
      .join("\n\n")
      .trim(),
  );

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  if (!file || !text) return null;

  const onClick = async () => {
    if (state !== "idle") return;
    setState("pending");
    setError("");
    try {
      await file(text);
      setState("done");
      timer.current = setTimeout(() => setState("idle"), pinAckMs);
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      console.error("pin failed", err);
      setError(detail);
      setState("idle");
    }
  };

  return (
    <TooltipIconButton
      tooltip={
        error
          ? t("thread.pinFailed", { error })
          : state === "pending"
            ? t("thread.pinPending")
            : state === "done"
              ? t("thread.pinQueued")
              : t("thread.pin")
      }
      onClick={onClick}
      disabled={state !== "idle"}
      aria-busy={state === "pending"}
    >
      {state === "pending" ? (
        <span
          className="border-current/30 border-t-current size-3.5 animate-spin rounded-full border-2"
          role="status"
        />
      ) : state === "done" ? (
        <CheckIcon className="animate-in zoom-in-50 fade-in animate-pulse text-emerald-500 duration-200 ease-out" />
      ) : error ? (
        <AlertCircleIcon className="text-destructive" />
      ) : (
        <PinIcon />
      )}
    </TooltipIconButton>
  );
};
