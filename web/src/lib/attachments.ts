import { api, enc } from "./api";
import { uuid } from "./format";
import { t } from "../i18n";

export const MAX_IMAGE = 8 * 1024 * 1024;
export const MAX_FILE = 200 * 1024 * 1024;

export interface LocalAttachment {
  id: string;
  name: string;
  mime: string;
  file: File;
  url?: string;
}

export function pickAttachment(file: File): LocalAttachment {
  const image = file.type.startsWith("image/");
  if (file.size > (image ? MAX_IMAGE : MAX_FILE)) {
    throw new Error(t(image ? "lib-attachments.too_big_image" : "lib-attachments.too_big_file", { name: file.name }));
  }
  return { id: uuid(), name: file.name || "image.png", mime: file.type || "application/octet-stream", file, url: image ? URL.createObjectURL(file) : undefined };
}

export async function imagePayload(file: File): Promise<{ mime: string; data: string }> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return { mime: file.type || "image/png", data: btoa(bin) };
}

/**
 * Sends a file to machine `mid` (POST /machines/{mid}/files, docs/protocol/protocol.md "Sending files"): the chat's attachments, a new
 * agent's files and the terminal's pasted ones. Resolves to the file's absolute path on that machine.
 */
export async function sendFile(mid: string, file: File, opts: { name?: string; dest?: "terminal" | "health"; signal?: AbortSignal } = {}): Promise<string> {
  const query = `name=${enc(opts.name ?? file.name)}${opts.dest ? `&dest=${opts.dest}` : ""}`;
  const r = await api<{ path: string }>(`/machines/${enc(mid)}/files?${query}`, {
    method: "POST",
    signal: opts.signal,
    body: file,
    headers: { "Content-Type": "application/octet-stream" },
  });
  return r.path;
}

export function quotePath(path: string) {
  return /[\s'"\\$`]/.test(path) ? `'${path.replace(/'/g, `'\\''`)}'` : path;
}

/** Frees the pictures' object URLs, an edited one's source too (lib/image-edit.ts EditedImage): the composer made
 *  every `blob:` source it holds (a picture in the chat edited from the viewer keeps its own URL, never a blob). */
export function releaseAttachments(attachments: { url?: string; edited?: { source: string } }[]) {
  attachments.forEach((a) => {
    if (a.url) URL.revokeObjectURL(a.url);
    if (a.edited?.source.startsWith("blob:") && a.edited.source !== a.url) URL.revokeObjectURL(a.edited.source);
  });
}
