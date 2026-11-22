// Splits a rendered system prompt into the pieces it was assembled from, for
// the inspector's collapsed view. Two boundaries exist in the text:
//
//   - an injected block's YAML header, a `---` line followed by `type: X`
//     (agent_identity, people_knowledge, user_preference, …);
//   - an H1 (`# Context`, `# Tools`, …), which is how system-prompt sections
//     and most block bodies open.
//
// A header and the H1 directly under it are ONE section: the header says what
// the block is, the H1 names it. So an H1 starts a new section only once the
// current one already has a heading. Lines inside a code fence never split.
//
// Sections are contiguous slices of the original lines, so joining every
// section's text with "\n" reproduces the prompt exactly.

export type PromptSection = {
  /** First H1 in the section, else the block type, else "". */
  title: string;
  /** `type:` of the injected block this section belongs to, if any. */
  blockType: string;
  text: string;
};

const FENCE = /^\s*(```|~~~)/;

export function splitPromptSections(prompt: string): PromptSection[] {
  const lines = prompt.split("\n");
  const sections: PromptSection[] = [];
  let cur: { start: number; title: string; blockType: string } | null = null;
  let blockType = "";
  let inFence = false;
  let inHeader = false;

  const close = (end: number) => {
    if (!cur || end <= cur.start) return;
    sections.push({
      title: cur.title || cur.blockType,
      blockType: cur.blockType,
      text: lines.slice(cur.start, end).join("\n"),
    });
  };
  const open = (i: number, title: string) => {
    close(i);
    cur = { start: i, title, blockType };
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    if (inHeader) {
      if (line === "---") inHeader = false;
      continue;
    }
    if (FENCE.test(line)) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;

    if (line === "---" && /^type:\s*\S/.test(lines[i + 1] ?? "")) {
      blockType = lines[i + 1].replace(/^type:\s*/, "").trim();
      inHeader = true;
      open(i, "");
      continue;
    }
    if (line.startsWith("# ")) {
      const title = line.slice(2).trim();
      if (cur && !(cur as { title: string }).title) {
        (cur as { title: string }).title = title;
      } else {
        open(i, title);
      }
      continue;
    }
    if (!cur) open(i, "");
  }
  close(lines.length);
  return sections;
}
