import { useSyncExternalStore } from "react";
import Compressor from "compressorjs";
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
// File immediately); send() shrinks it to the long edge the providers bill for,
// uploads the bytes to /api/media, and stores the returned basename as an
// ImageMessagePart whose `image` is the media URL. The chat send path (onNew)
// reads that URL back to a basename and forwards it on the "message" WS frame,
// which the backend turns into a media_summary.
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

// Long edge the providers bill for (provider/image_tokens.go's
// imageMaxLongEdge). An image larger than this costs the same tokens as one
// exactly this size and is downscaled to it before the model ever sees it, so
// every pixel past the cap is upload time bought for nothing. Measured on this
// deployment's own traffic: 28 of 32 web uploads were over it, median long edge
// 4032px, largest 6.18MB at 5712x4284.
const maxLongEdge = 1568;

// Below this, re-encoding costs more than it saves: a second lossy pass on an
// image that already uploads in well under a second. The threshold is in BYTES
// rather than pixels on purpose — the point of shrinking here is upload time,
// and enforcing the pixel cap is the provider's job, not ours.
const shrinkFloorBytes = 512 * 1024;

// shrink re-encodes an oversized image down to maxLongEdge. Every option below
// was set against a measurement, so none should change without redoing one; the
// same 5712x4284 iPhone photo was run through both engines Playwright ships.
//
// compressorjs is used rather than a hand-written canvas pass for ONE reason
// that appears nowhere in its docs: it draws from an `<img>` element, and in
// WebKit that produces different pixels than `createImageBitmap` for a photo
// carrying an ICC profile — which is every iPhone photo. Measured centre pixel
// [60,49,81] via `<img>` against [43,28,64] via createImageBitmap, and visibly
// the bitmap path crushes shadow detail; on a photo of a shelf of price labels
// that is exactly the detail the model is being asked to read. Chromium's two
// paths agree, so the difference is invisible unless Safari is tested. A future
// rewrite onto createImageBitmap — more modern, and the only path that works
// inside a Worker — would silently reintroduce it.
async function shrink(file: File): Promise<Blob> {
  if (file.size <= shrinkFloorBytes) return file;

  let out: Blob;
  try {
    out = await new Promise<Blob>((resolve, reject) => {
      new Compressor(file, {
        quality: 0.85,
        maxWidth: maxLongEdge,
        maxHeight: maxLongEdge,
        // Both engines already apply EXIF orientation on every decode path, so
        // compressorjs's own EXIF handling is pure overhead: it reads the whole
        // file into an ArrayBuffer, patches the orientation tag, and base64s the
        // result into a data URL. Measured identical output bytes and about a
        // third of the time with it off — 622ms to 217ms on Chromium, 357ms to
        // 99ms on WebKit.
        checkOrientation: false,
        // Never turn a PNG into a JPEG. The default converts PNGs over 5MB,
        // which silently composites transparency onto black; the documented
        // alternative — a beforeDraw white fill — destroys the transparency just
        // as surely. A PNG stays a PNG and only loses pixels, and if that fails
        // to shrink it the guard below hands back the original.
        convertSize: Infinity,
        success: resolve,
        error: reject,
      });
    });
  } catch {
    // A failed shrink is not a failed send: the original is always a valid thing
    // to upload, so this degrades to the pre-compression behaviour rather than
    // costing the user their message.
    return file;
  }

  // compressorjs's own `strict` option is meant to be this check and is not:
  // its condition is short-circuited by `maxWidth < naturalWidth`, i.e. by the
  // very resize being asked for, so it never fires here. It has to be done by
  // hand, and it genuinely bites — a 2600x2600 / 202KB transparent PNG came back
  // from WebKit at 1576KB, 7.8x LARGER, and compressorjs delivered it.
  if (out.size >= file.size) return file;
  // A changed type means an option above did something other than what its
  // comment claims. Upload what is understood, not what was assumed.
  if (out.type !== file.type) return file;
  return out;
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

    // Shrink before uploading. The chip is already on screen at 0% for this
    // stretch, which is honest: no bytes have moved yet.
    const body = await shrink(attachment.file);

    let uploaded: string;
    try {
      uploaded = (
        await uploadMedia(body, (loaded, total) => {
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
