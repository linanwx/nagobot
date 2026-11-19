import { ChevronDownIcon, Sparkles } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  fetchSessionSkill,
  fetchSessionSkills,
  type SessionSkill,
} from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

// Which skills this session has in effect, and what each one says.
//
// The skill registry is global — every agent in every session is offered the
// same set — so the session-specific part is the top section: skills this
// session loaded with use_skill, each marked by whether a loaded body is still
// sent to the model or Tier-1 compression has reduced it to a header. Every
// other registered skill sits below, collapsed.
//
// A body is the SKILL.md on disk now, resolved for this session exactly as
// use_skill would resolve it. It is shown raw, like the system prompt: the
// point is to read what the model is given, literal placeholders and all.

function Tag({ tone, children }: { tone: "on" | "off" | "warn"; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded px-1.5 py-px text-[10px] font-medium",
        tone === "on" && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
        tone === "off" && "bg-muted text-muted-foreground",
        tone === "warn" && "bg-destructive/10 text-destructive",
      )}
    >
      {children}
    </span>
  );
}

function SkillRow({ sessionKey, skill }: { sessionKey: string; skill: SessionSkill }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState<string | null>(null);
  const [error, setError] = useState("");

  // Fetched on first expand and kept for the life of the dialog: bodies run to
  // tens of KB each, and the list does not carry them.
  useEffect(() => {
    if (!open || body !== null || !skill.registered) return;
    let cancelled = false;
    fetchSessionSkill(sessionKey, skill.slug)
      .then((d) => {
        if (!cancelled) setBody(d.prompt);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, body, sessionKey, skill.slug, skill.registered]);

  return (
    <div className="border-border shrink-0 overflow-hidden rounded-md border">
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            className="hover:bg-muted/50 dark:hover:bg-muted/50 h-auto w-full items-start justify-start gap-1.5 rounded-none px-2 py-1.5 text-start font-normal whitespace-normal max-md:h-auto max-md:min-h-10"
          >
            <ChevronDownIcon
              className={cn(
                "text-muted-foreground mt-0.5 size-3.5 shrink-0 transition-transform",
                !open && "-rotate-90",
              )}
            />
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="font-mono text-sm font-medium">{skill.slug}</span>
                {skill.loaded &&
                  (skill.in_context ? (
                    <Tag tone="on">{t("skills.inContext")}</Tag>
                  ) : (
                    <Tag tone="off">{t("skills.compressed")}</Tag>
                  ))}
                {!skill.registered && <Tag tone="warn">{t("skills.removed")}</Tag>}
              </span>
              {skill.description && (
                <span className="text-muted-foreground mt-0.5 line-clamp-2 block text-xs">
                  {skill.description}
                </span>
              )}
            </span>
            {skill.loaded && skill.last_loaded_at && (
              <span
                className="text-muted-foreground shrink-0 pt-0.5 text-[10px]"
                title={new Date(skill.last_loaded_at).toLocaleString()}
              >
                {t("skills.loads", { count: skill.load_count ?? 1 })} ·{" "}
                {relativeTime(skill.last_loaded_at)}
              </span>
            )}
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className="border-border/60 border-t px-3 py-2">
            {!skill.registered ? (
              <p className="text-muted-foreground text-xs">{t("skills.removedBody")}</p>
            ) : error ? (
              <p className="text-destructive text-xs">{error}</p>
            ) : body === null ? (
              <p className="text-muted-foreground text-xs">{t("common.loading")}</p>
            ) : (
              <pre className="max-h-[50dvh] overflow-auto font-mono text-xs leading-relaxed whitespace-pre-wrap break-words">
                {body}
              </pre>
            )}
          </div>
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

export function SkillsDialog({
  sessionKey,
  open,
  onOpenChange,
}: {
  sessionKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [skills, setSkills] = useState<SessionSkill[] | null>(null);
  const [error, setError] = useState("");
  const [othersOpen, setOthersOpen] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setSkills(null);
    setError("");
    setOthersOpen(false);
    fetchSessionSkills(sessionKey)
      .then((list) => {
        if (!cancelled) setSkills(list);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionKey]);

  const loaded = skills?.filter((s) => s.loaded) ?? [];
  const others = skills?.filter((s) => !s.loaded) ?? [];
  const inContext = loaded.filter((s) => s.in_context).length;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Sparkles className="size-4" />
            {t("skills.title")}
          </DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {sessionKey}
            {skills &&
              ` · ${t("skills.counts", { loaded: loaded.length, inContext, total: skills.length })}`}
          </DialogDescription>
        </DialogHeader>
        {error && <p className="text-destructive text-xs">{error}</p>}
        {skills === null ? (
          !error && <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain pe-1">
            <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
              {t("skills.loadedHeading")}
            </h3>
            {loaded.length === 0 ? (
              <p className="text-muted-foreground text-sm">{t("skills.noneLoaded")}</p>
            ) : (
              loaded.map((s) => <SkillRow key={s.slug} sessionKey={sessionKey} skill={s} />)
            )}
            {others.length > 0 && (
              <Collapsible open={othersOpen} onOpenChange={setOthersOpen} className="mt-2">
                <CollapsibleTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground -ms-1.5 gap-1 px-1.5 text-xs font-medium tracking-wide uppercase"
                  >
                    <ChevronDownIcon
                      className={cn("size-3.5 transition-transform", !othersOpen && "-rotate-90")}
                    />
                    {t("skills.othersHeading", { count: others.length })}
                  </Button>
                </CollapsibleTrigger>
                <CollapsibleContent className="mt-2 flex flex-col gap-2">
                  {others.map((s) => (
                    <SkillRow key={s.slug} sessionKey={sessionKey} skill={s} />
                  ))}
                </CollapsibleContent>
              </Collapsible>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
