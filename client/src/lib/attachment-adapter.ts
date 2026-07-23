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
export const imageAttachmentAdapter: AttachmentAdapter = {
  accept: "image/*",

  async add({ file }): Promise<PendingAttachment> {
    return {
      id: crypto.randomUUID(),
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
