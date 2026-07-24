import type {
  AttachmentAdapter,
  CompleteAttachment,
  PendingAttachment,
} from "@assistant-ui/react";
import { mediaURL, uploadMedia } from "@/lib/api";

// imageAttachmentAdapter wires the composer's attachment UI (the "+" button, the
// drag-drop dropzone, AND clipboard paste — assistant-ui routes pasted images
// through the same adapter) to the nagobot backend.
//
// Flow: add() registers a local pending image (renders a thumbnail from the
// File immediately); send() uploads the bytes to /api/media and stores the
// returned basename as an ImageMessagePart whose `image` is the media URL. The
// chat send path (onNew) reads that URL back to a basename and forwards it on
// the "message" WS frame, which the backend turns into a media_summary.
//
// Only images are accepted — the backend upload endpoint rejects everything
// else, so keeping `accept` in sync avoids a picker that offers unsendable
// files.
// randomID returns a unique attachment id. crypto.randomUUID is unavailable
// outside secure contexts (a plain-http origin, e.g. reaching the daemon by LAN
// IP), where reading it throws and takes the whole composer down with it —
// crypto.getRandomValues has no such restriction, so it carries the fallback.
// The id is a local list key, never persisted, so exact UUID shape is moot.
function randomID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export const imageAttachmentAdapter: AttachmentAdapter = {
  accept: "image/*",

  async add({ file }): Promise<PendingAttachment> {
    return {
      id: randomID(),
      type: "image",
      name: file.name,
      contentType: file.type,
      file,
      status: { type: "running", reason: "uploading", progress: 0 },
    };
  },

  async send(attachment): Promise<CompleteAttachment> {
    const { name } = await uploadMedia(attachment.file);
    return {
      ...attachment,
      status: { type: "complete" },
      // The URL doubles as the carrier for the uploaded basename: onNew parses
      // it back out. mediaURL encodes the name; onNew decodes it.
      content: [{ type: "image", image: mediaURL(name) }],
    };
  },

  async remove(): Promise<void> {
    // Uploads are cheap and land in the shared media dir that also backs
    // delivered messages; there's nothing session-specific to revoke on
    // removal, so this is a no-op.
  },
};
