import { BookText, Settings } from "lucide-react";
import { useEffect, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { ConfigView } from "@/components/config-view";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  fetchConfig,
  fetchPromptFile,
  fetchPromptFiles,
  type PromptFileEntry,
} from "@/lib/api";
import { cn } from "@/lib/utils";

// Compact markdown styling for prompt documents. Kept local: the registry
// MarkdownText only renders inside assistant message parts.
const docMarkdownComponents: Components = {
  h1: (p) => <h2 className="mt-3 mb-1.5 text-base font-semibold first:mt-0" {...p} />,
  h2: (p) => <h3 className="mt-3 mb-1.5 text-sm font-semibold first:mt-0" {...p} />,
  h3: (p) => <h4 className="mt-2 mb-1 text-sm font-semibold first:mt-0" {...p} />,
  p: (p) => <p className="mb-2 last:mb-0" {...p} />,
  ul: (p) => <ul className="mb-2 list-disc ps-5 last:mb-0" {...p} />,
  ol: (p) => <ol className="mb-2 list-decimal ps-5 last:mb-0" {...p} />,
  blockquote: (p) => (
    <blockquote
      className="border-border text-muted-foreground mb-2 border-s-2 ps-3 italic"
      {...p}
    />
  ),
  a: (p) => (
    <a className="underline underline-offset-2" target="_blank" rel="noreferrer" {...p} />
  ),
  code: (p) => <code className="bg-muted rounded px-1 font-mono text-xs" {...p} />,
  pre: (p) => (
    <pre
      className="bg-muted mb-2 overflow-x-auto rounded-md p-3 font-mono text-xs last:mb-0"
      {...p}
    />
  ),
  hr: () => <hr className="border-border/60 my-3" />,
  table: (p) => (
    <div className="mb-2 w-full overflow-x-auto last:mb-0">
      <table className="text-xs" {...p} />
    </div>
  ),
  th: (p) => <th className="border-border border px-2 py-1 text-start" {...p} />,
  td: (p) => <td className="border-border border px-2 py-1" {...p} />,
};

function ConfigDialog() {
  const [open, setOpen] = useState(false);
  const [config, setConfig] = useState<unknown>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    fetchConfig()
      .then(setConfig)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="size-7">
          <Settings className="size-4" />
          <span className="sr-only">Daemon configuration</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Configuration</DialogTitle>
          <DialogDescription>
            Live daemon config (read-only; secrets redacted).
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <p className="text-destructive text-sm">{error}</p>
        ) : config == null ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pe-1">
            <ConfigView config={config} />
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function PromptsDialog() {
  const [open, setOpen] = useState(false);
  const [files, setFiles] = useState<PromptFileEntry[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const selectedEntry = files?.find((f) => f.name === selected) ?? null;

  useEffect(() => {
    if (!open) return;
    setError(null);
    fetchPromptFiles()
      .then((list) => {
        // Server returns a curated whitelist in display order.
        setFiles(list);
        if (list.length > 0) setSelected((cur) => cur ?? list[0].name);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [open]);

  useEffect(() => {
    if (!open || !selected) return;
    setContent(null);
    fetchPromptFile(selected)
      .then(setContent)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [open, selected]);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="size-7">
          <BookText className="size-4" />
          <span className="sr-only">Global prompts</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Global prompts</DialogTitle>
          <DialogDescription>
            System prompt files injected into every agent (read-only).
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <p className="text-destructive text-sm">{error}</p>
        ) : files == null ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : files.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No prompt files found (or the daemon predates /api/prompts).
          </p>
        ) : (
          <>
            <div className="flex shrink-0 flex-wrap gap-1.5">
              {files.map((f) => (
                <button
                  key={f.name}
                  type="button"
                  onClick={() => setSelected(f.name)}
                  className={cn(
                    "rounded-full border px-2.5 py-0.5 text-xs",
                    selected === f.name
                      ? "border-transparent bg-primary text-primary-foreground"
                      : "border-border text-muted-foreground hover:bg-muted",
                  )}
                >
                  {f.label}
                </button>
              ))}
            </div>
            {selectedEntry && (
              <p className="text-muted-foreground shrink-0 text-xs">
                {selectedEntry.description}
                <span className="text-muted-foreground/60">
                  {" · "}
                  {selectedEntry.name}
                </span>
              </p>
            )}
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-md border p-3 text-sm leading-relaxed wrap-break-word">
              {content == null ? (
                <p className="text-muted-foreground">Loading…</p>
              ) : (
                <ReactMarkdown remarkPlugins={[remarkGfm]} components={docMarkdownComponents}>
                  {content}
                </ReactMarkdown>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// SidebarFooter is the strip at the bottom of the session list: daemon
// configuration and the global prompt files, both read-only viewers.
export function SidebarFooter() {
  return (
    <div className="flex shrink-0 items-center gap-1 border-t px-2 py-1.5">
      <ConfigDialog />
      <PromptsDialog />
    </div>
  );
}
