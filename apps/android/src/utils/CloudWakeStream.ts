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
  private xhr: XMLHttpRequest | null = null;
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
    this.xhr?.abort();
    this.xhr = null;
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
      this.xhr?.abort();
    }, SILENCE_TIMEOUT_MS);
  }

  private scheduleReconnect(): void {
    if (!this.running) return;
    // Exponential, capped, so a server that is down is not hammered by a fleet
    // of tablets all retrying together.
    const delay = Math.min(RETRY_MIN_MS * 2 ** Math.min(this.failures, 5), RETRY_MAX_MS);
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  private connect(): void {
    if (!this.running) return;
    getCloudCredentials()
      .catch(() => null)
      .then(c => {
        if (!this.running) return;
        if (!c) {
          // Not enrolled yet. Try again later rather than giving up for good.
          this.failures++;
          this.scheduleReconnect();
          return;
        }
        this.open(c);
      });
  }

  /**
   * Open the stream with XMLHttpRequest rather than fetch.
   *
   * React Native's fetch does not implement Response.body — there is no
   * ReadableStream to read, so a streaming response is buffered whole and
   * delivered only when the connection ends. For a stream deliberately held
   * open for minutes that means every notice arrives minutes late, which is
   * worse than not having it: from the outside it looks like it works.
   *
   * XHR exposes the partial body through responseText at readyState 3, which is
   * how server-sent events have always been read on this platform.
   */
  private open(c: CloudCredentials): void {
    const xhr = new XMLHttpRequest();
    this.xhr = xhr;
    let consumed = 0;
    let settled = false;

    const finish = (why: string) => {
      if (settled) return;
      settled = true;
      this.connected = false;
      this.xhr = null;
      if (this.silenceTimer) {
        clearTimeout(this.silenceTimer);
        this.silenceTimer = null;
      }
      if (!this.running) return;
      if (why !== 'bye') {
        this.failures++;
        if (this.failures === 1 || this.failures % 10 === 0) {
          console.warn(`[WakeStream] Disconnected (${this.failures}): ${why}`);
        }
      }
      this.scheduleReconnect();
    };

    xhr.onreadystatechange = () => {
      // 3 = LOADING: the body is arriving, and responseText grows with it.
      if (xhr.readyState === 3 || xhr.readyState === 4) {
        if (xhr.status === 200) {
          if (!this.connected) {
            this.connected = true;
            this.failures = 0;
          }
          this.heard();
          const text = xhr.responseText ?? '';
          if (text.length > consumed) {
            const fresh = text.slice(consumed);
            consumed = text.length;
            if (fresh.includes('event: wake')) this.onWake();
            if (fresh.includes('event: bye')) {
              // The server is recycling the connection. Reconnect at once
              // rather than waiting to discover a dead socket.
              this.failures = 0;
              xhr.abort();
              finish('bye');
              return;
            }
          }
        }
      }
      if (xhr.readyState === 4) {
        finish(xhr.status === 200 ? 'closed' : `HTTP ${xhr.status}`);
      }
    };
    xhr.onerror = () => finish('network error');
    xhr.ontimeout = () => finish('timeout');
    xhr.onabort = () => finish(this.running ? 'aborted' : 'stopped');

    try {
      xhr.open('GET', `${c.cloudUrl}/api/v1/devices/${c.deviceId}/events`);
      xhr.setRequestHeader('Authorization', `Bearer ${c.apiKey}`);
      xhr.setRequestHeader('Accept', 'text/event-stream');
      xhr.setRequestHeader('Cache-Control', 'no-cache');
      xhr.send();
      this.heard();
    } catch (error: any) {
      finish(error?.message ?? String(error));
    }
  }

}

export const CloudWakeStream = new CloudWakeStreamImpl();
export default CloudWakeStream;
