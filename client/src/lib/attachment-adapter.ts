import { useSyncExternalStore } from "react";
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
// The upload also publishes its progress here, and that exists to close a window
// in which the UI showed nothing at all. The composer clears the text and the
// attachment chips BEFORE awaiting send() (base-composer-runtime-core's send()),
// while the chat hook's pending chip is created inside onNew, which the composer
// only reaches once every attachment has resolved. So between the click and the
// last byte there was no input, no chip, no attachment and no spinner — and a
// 6MB phone photo is seconds of that. useActiveUploads lets the hook render the
// upload as a queue chip for exactly that gap, so the click is acknowledged
// immediately and the message chip takes over the moment the bytes land.
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

// UploadState is one in-flight (or just-failed) upload, as the queue chip shows
// it: the file's own name, and either how far along it is or why it stopped.
export type UploadState = {
  id: string;
  name: string;
  percent: number;
  error?: string;
};

const active = new Map<string, UploadState>();
const listeners = new Set<() => void>();

// useSyncExternalStore compares snapshots by identity and re-renders forever if
// the getter builds a new array each call, so the array is rebuilt only when the
// map actually changes.
const EMPTY: readonly UploadState[] = [];
let snapshot: readonly UploadState[] = EMPTY;

function publish() {
  snapshot = active.size === 0 ? EMPTY : [...active.values()];
  for (const l of listeners) l();
}

function subscribe(onChange: () => void) {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

// useActiveUploads feeds the queue chips. It is a subscription rather than a
// prop because the adapter is a module singleton owned by the composer — React
// never sees send() being called.
export function useActiveUploads(): readonly UploadState[] {
  return useSyncExternalStore(
    subscribe,
    () => snapshot,
    () => EMPTY,
  );
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
      // Nothing is uploading yet: send() does that, and the composer only
      // calls it once the user hits send. Saying "running/uploading" here
      // paints a spinner over the thumbnail for as long as the image sits in
      // the composer, which is a lie about work that has not started.
      status: { type: "requires-action", reason: "composer-send" },
    };
  },

  async send(attachment): Promise<CompleteAttachment> {
    const { id, name } = attachment;
    // Overwrites any failure left from a previous attempt: the composer restored
    // this attachment when the last send threw, so a retry is the same id.
    active.set(id, { id, name, percent: 0 });
    publish();

    let uploaded: string;
    try {
      uploaded = (
        await uploadMedia(attachment.file, (loaded, total) => {
          // Capped below 100 because upload.onprogress reaching the end means
          // the bytes left this machine, not that the server accepted them.
          // Showing 100% and then sitting there is the same lie as showing
          // nothing — the chip retires when send() resolves, not before.
          const percent =
            total > 0 ? Math.min(99, Math.floor((loaded / total) * 100)) : 0;
          const cur = active.get(id);
          if (!cur || cur.percent === percent) return;
          active.set(id, { ...cur, percent });
          publish();
        })
      ).name;
    } catch (e) {
      // Kept in the store rather than deleted. The composer's own catch restores
      // the text and the attachment and rethrows into nothing, so removing the
      // chip here would leave a failed upload with no trace at all: the message
      // simply never appears and the user is not told why.
      const cur = active.get(id);
      const message = e instanceof Error ? e.message : String(e);
      active.set(id, { ...(cur ?? { id, name, percent: 0 }), error: message });
      publish();
      throw e;
    }

    active.delete(id);
    publish();
    return {
      ...attachment,
      status: { type: "complete" },
      // The URL doubles as the carrier for the uploaded basename: onNew parses
      // it back out. mediaURL encodes the name; onNew decodes it.
      content: [{ type: "image", image: mediaURL(uploaded) }],
    };
  },

  async remove(attachment): Promise<void> {
    // Clears a failed upload's chip: dropping the attachment is the user saying
    // they are done with it. Bytes that did reach the server are left alone —
    // the media dir is shared with delivered messages and holds nothing
    // session-specific to revoke.
    if (active.delete(attachment.id)) publish();
  },
};
