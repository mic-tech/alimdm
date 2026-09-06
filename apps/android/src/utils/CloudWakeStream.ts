/**
 * CloudWakeStream.ts
 *
 * Holds a server-sent events connection open so the cloud can say "there is
 * work" the moment there is, instead of the tablet finding out on its next
 * thirty-second heartbeat.
 *
 * The stream carries no instructions. Every notice is empty and the answer to
 * all of them is the same — heartbeat now — which is what the device would have
 * done anyway, just sooner. That is the whole design: a stream that drops,
 * stalls, is blocked by a school firewall or never connects at all costs
 * latency and nothing else. The poll remains the mechanism; this only makes it
 * punctual.
 *
 * Nothing here retries work or tracks delivery. If a notice is missed the next
 * heartbeat collects the same work, in the same order, with no state to have
 * got out of step.
 */
import { getCloudCredentials, CloudCredentials } from './secureStorage';

/** How long to wait before reconnecting, growing with consecutive failures. */
const RETRY_MIN_MS = 5_000;
const RETRY_MAX_MS = 120_000;

/**
 * Give up on a connection that has gone quiet. The server sends a keepalive
 * every 20s, so silence well past that means the connection is dead in a way
 * neither end has noticed — the classic half-open socket after a network
 * change, which otherwise sits there looking healthy for ever.
 */
const SILENCE_TIMEOUT_MS = 70_000;

type WakeHandler = () => void;

class CloudWakeStreamImpl {
  private running = false;
  private failures = 0;
  private controller: AbortController | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private silenceTimer: ReturnType<typeof setTimeout> | null = null;
  private onWake: WakeHandler = () => {};

  /** Whether a connection is currently established. */
  connected = false;

  start(onWake: WakeHandler): void {
    if (this.running) return;
    this.running = true;
    this.onWake = onWake;
    this.connect();
  }

  stop(): void {
    this.running = false;
    this.clearTimers();
    this.controller?.abort();
    this.controller = null;
    this.connected = false;
  }

  private clearTimers(): void {
    if (this.retryTimer) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
    if (this.silenceTimer) {
      clearTimeout(this.silenceTimer);
      this.silenceTimer = null;
    }
  }

  /** Restart the silence watchdog; any byte from the server counts as alive. */
  private heard(): void {
    if (this.silenceTimer) clearTimeout(this.silenceTimer);
    this.silenceTimer = setTimeout(() => {
      console.warn('[WakeStream] No keepalive for 70s — reconnecting');
      this.controller?.abort();
    }, SILENCE_TIMEOUT_MS);
  }

  private scheduleReconnect(): void {
    if (!this.running) return;
    // Exponential, capped, so a server that is down is not hammered by a fleet
    // of tablets all retrying together.
    const delay = Math.min(RETRY_MIN_MS * 2 ** Math.min(this.failures, 5), RETRY_MAX_MS);
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  private async connect(): Promise<void> {
    if (!this.running) return;
    let c: CloudCredentials | null = null;
    try {
      c = await getCloudCredentials();
    } catch {
      c = null;
    }
    if (!c) {
      // Not enrolled yet. Try again later rather than giving up for good.
      this.failures++;
      this.scheduleReconnect();
      return;
    }

    const controller = new AbortController();
    this.controller = controller;
    try {
      const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/events`, {
        method: 'GET',
        headers: {
          Authorization: `Bearer ${c.apiKey}`,
          Accept: 'text/event-stream',
          'Cache-Control': 'no-cache',
        },
        signal: controller.signal,
        // React Native's fetch buffers the whole body by default, which would
        // hold every notice until the connection ended. This asks for the raw
        // stream instead.
        reactNative: { textStreaming: true },
      } as RequestInit);

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      // A 401 would keep repeating; anything else is worth counting as success
      // for backoff purposes the moment the connection is established.
      this.failures = 0;
      this.connected = true;
      this.heard();
      await this.read(res);
    } catch (error: any) {
      if (controller.signal.aborted && !this.running) return; // deliberate stop
      this.failures++;
      if (this.failures === 1 || this.failures % 10 === 0) {
        console.warn(`[WakeStream] Disconnected (${this.failures}): ${error?.message ?? error}`);
      }
    } finally {
      this.connected = false;
      this.controller = null;
      if (this.silenceTimer) {
        clearTimeout(this.silenceTimer);
        this.silenceTimer = null;
      }
      this.scheduleReconnect();
    }
  }

  /**
   * Read the stream, firing onWake for every wake event.
   *
   * SSE frames are separated by a blank line and we only care about one event
   * name, so this looks for the line rather than parsing the format properly.
   * There is nothing else on this stream to misread.
   */
  private async read(res: Response): Promise<void> {
    const body: any = (res as any).body;
    if (!body?.getReader) {
      // No streaming support in this environment: treat the whole response as
      // one blob when it eventually ends, and let the reconnect loop carry on.
      const text = await res.text();
      if (text.includes('event: wake')) this.onWake();
      return;
    }
    const reader = body.getReader();
    let buffer = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      this.heard();
      // textStreaming hands back strings. Anything else is coerced rather than
      // decoded: this stream is ASCII event names, so there is no multi-byte
      // character to split across a chunk boundary.
      buffer += typeof value === 'string' ? value : String.fromCharCode(...(value ?? []));

      // Keep only the tail after the last frame boundary, so a frame split
      // across two reads is still seen whole.
      let boundary = buffer.indexOf('\n\n');
      while (boundary !== -1) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        if (frame.includes('event: wake')) {
          this.onWake();
        }
        // The server closes the connection after this; reconnect at once
        // rather than waiting to discover a dead socket.
        if (frame.includes('event: bye')) {
          this.failures = 0;
          return;
        }
        boundary = buffer.indexOf('\n\n');
      }
      // A frame larger than anything this stream sends means something is
      // wrong upstream; drop it rather than growing without bound.
      if (buffer.length > 8192) buffer = '';
    }
  }
}

export const CloudWakeStream = new CloudWakeStreamImpl();
export default CloudWakeStream;
