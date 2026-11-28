import { apiFetch } from "@kukeon/api-client";
import { apiPath } from "./routes";
import { createLineSplitter, type LineSplitter } from "./recording.mjs";

// Loads a container's whole terminal recording for the Replay view (issue #1783) from
// GET …/containers/{name}/recording and …/recording/segments/{seq}: every retained segment, oldest
// first, decompressing the closed ones in the browser. Lines (asciicast) or bytes (raw) are handed
// to the caller as they arrive, so playback can start before the last segment does. While the cell
// is live, the live segment is followed by offset; a rotation (the segment reads live: false) or a
// pruned segment (segment_gone) is answered by re-listing, and a re-read segment drops what is held.

export type RecordingFormat = "asciicast" | "raw";
type Segment = { seq: number; size: number; compressed: boolean; live: boolean };
type Listing = { format: RecordingFormat; segments: Segment[] };
type Chunk = { data?: string | null; start: number; next: number; size: number; more: boolean; compressed: boolean; live: boolean };
export type SourceError = { code: string; message: string };

export type SourceHandlers = {
  // The listing arrived: the format, and whether retention dropped earlier output.
  begin(format: RecordingFormat, dropped: boolean): void;
  // Complete asciicast lines, in order, each once.
  lines(lines: string[]): void;
  // Raw output bytes, in order, each once.
  bytes(bytes: Uint8Array): void;
  // Loading progress: segments read of those listed, and whether the initial load is done.
  progress(read: number, total: number, loaded: boolean): void;
  error(error: SourceError): void;
};

const MAX_BYTES = 1 << 20;
export const FOLLOW_INTERVAL_MS = 2000;
// Lines handed over in one batch before yielding to the browser, so the controls stay usable.
const LINE_BATCH = 2000;

function decode(data?: string | null): Uint8Array {
  if (!data) return new Uint8Array();
  const text = atob(data);
  const bytes = new Uint8Array(text.length);
  for (let i = 0; i < text.length; i++) bytes[i] = text.charCodeAt(i);
  return bytes;
}
const yieldToBrowser = () => new Promise<void>(resolve => setTimeout(resolve, 0));

async function gunzip(bytes: Uint8Array): Promise<Uint8Array> {
  const stream = new Blob([bytes as BlobPart]).stream().pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

function concat(parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((sum, part) => sum + part.length, 0));
  let at = 0;
  for (const part of parts) { out.set(part, at); at += part.length; }
  return out;
}

// What has been taken from one segment: complete lines (asciicast) or bytes (raw).
type Held = { seq: number; splitter: LineSplitter; lines: number; bytes: number; offset: number };

export class RecordingSource {
  private readonly base: string;
  private readonly controller = new AbortController();
  private format: RecordingFormat = "asciicast";
  private held = new Map<number, Held>();
  private lastSeq = -1;
  private live: Held | null = null;
  private busy = false;
  private timer = 0;

  constructor(scope: string[], container: string, private readonly handlers: SourceHandlers, private readonly following: () => boolean) {
    this.base = `${apiPath(scope)}/containers/${encodeURIComponent(container)}/recording`;
  }

  start() {
    void this.run(async () => {
      const listing = await this.list();
      this.format = listing.format;
      this.handlers.begin(listing.format, (listing.segments[0]?.seq ?? 0) > 0);
      await this.readNew(listing, true);
    });
    this.timer = window.setInterval(() => {
      if (this.following() && document.visibilityState === "visible") void this.run(() => this.follow());
    }, FOLLOW_INTERVAL_MS);
  }

  close() {
    window.clearInterval(this.timer);
    this.controller.abort();
  }

  private async run(step: () => Promise<void>) {
    if (this.busy || this.controller.signal.aborted) return;
    this.busy = true;
    try { await step(); } catch (error) {
      if (!this.controller.signal.aborted) this.handlers.error(error as SourceError);
    } finally { this.busy = false; }
  }

  private async get<T>(path: string): Promise<T> {
    const response = await apiFetch(path, { signal: this.controller.signal, headers: { Accept: "application/json" } });
    let body: { error?: SourceError } & Partial<T> = {};
    try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
    if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
    return body as T;
  }

  private list() { return this.get<Listing>(this.base); }

  private chunk(seq: number, offset: number) {
    return this.get<Chunk>(`${this.base}/segments/${seq}?offset=${offset}&maxBytes=${MAX_BYTES}`);
  }

  // Reads the listed segments not read yet, oldest first: closed ones whole, the live one from its start.
  private async readNew(listing: Listing, initial: boolean) {
    const fresh = listing.segments.filter(segment => segment.seq > this.lastSeq || segment.seq === this.live?.seq);
    let read = 0;
    for (const segment of fresh) {
      if (this.controller.signal.aborted) return;
      if (segment.seq === this.live?.seq) { await this.follow(); read++; continue; }
      if (segment.live) {
        this.live = this.hold(segment.seq);
        // Rotated since it was listed: it is a closed segment now.
        if (!await this.readLive()) { this.live = null; await this.readClosed(segment.seq); }
      } else {
        await this.readClosed(segment.seq);
      }
      this.lastSeq = Math.max(this.lastSeq, segment.seq);
      read++;
      if (initial) this.handlers.progress(read, fresh.length, false);
    }
    if (initial) this.handlers.progress(fresh.length, fresh.length, true);
  }

  private hold(seq: number): Held {
    let held = this.held.get(seq);
    if (!held) { held = { seq, splitter: createLineSplitter(), lines: 0, bytes: 0, offset: 0 }; this.held.set(seq, held); }
    return held;
  }

  // A whole closed segment, decompressed; what was already taken from it while it was live is dropped.
  private async readClosed(seq: number) {
    const parts: Uint8Array[] = [];
    let offset = 0;
    let compressed = false;
    for (;;) {
      let chunk: Chunk;
      try { chunk = await this.chunk(seq, offset); } catch (error) {
        // Pruned between listing and read: nothing of it can be shown any more.
        if ((error as SourceError).code === "segment_gone") return;
        throw error;
      }
      compressed = chunk.compressed;
      parts.push(decode(chunk.data));
      offset = chunk.next;
      if (!chunk.more) break;
    }
    const stored = concat(parts);
    const bytes = compressed ? await gunzip(stored) : stored;
    const held = this.held.get(seq);
    this.held.delete(seq);
    if (this.format === "raw") {
      const rest = bytes.subarray(held?.bytes ?? 0);
      if (rest.length) this.handlers.bytes(rest);
      return;
    }
    const lines = createLineSplitter().push(bytes).slice(held?.lines ?? 0);
    for (let i = 0; i < lines.length; i += LINE_BATCH) {
      this.handlers.lines(lines.slice(i, i + LINE_BATCH));
      if (i + LINE_BATCH < lines.length) await yieldToBrowser();
    }
  }

  // Reads the live segment forward from the cursor. Returns false when it has rotated.
  private async readLive(): Promise<boolean> {
    const held = this.live!;
    for (;;) {
      const chunk = await this.chunk(held.seq, held.offset);
      if (!chunk.live) return false;
      // The live file was replaced (a new capture at the same seq): start over from its start.
      if (chunk.size < held.offset) { held.offset = 0; continue; }
      const bytes = decode(chunk.data);
      held.offset = chunk.next;
      if (this.format === "raw") {
        held.bytes += bytes.length;
        if (bytes.length) this.handlers.bytes(bytes);
      } else {
        const lines = held.splitter.push(bytes);
        held.lines += lines.length;
        if (lines.length) this.handlers.lines(lines);
      }
      if (!chunk.more) return true;
      await yieldToBrowser();
    }
  }

  private async follow() {
    if (!this.live) {
      // Nothing was live when listed (no output yet, or a stopped cell): look again.
      await this.readNew(await this.list(), false);
      return;
    }
    let current: boolean;
    try { current = await this.readLive(); } catch (error) {
      if ((error as SourceError).code !== "segment_gone") throw error;
      current = false;
    }
    if (current) return;
    // Rotated: the segment is closed now. Re-read it whole, dropping what is held, then pick up the
    // segments listed after it, the new live one included.
    const rotated = this.live.seq;
    this.live = null;
    await this.readClosed(rotated);
    this.lastSeq = Math.max(this.lastSeq, rotated);
    await this.readNew(await this.list(), false);
  }
}
