/**
 * Live view (tablet side).
 *
 * The capture-and-post loop is native; this only turns it on and off. The
 * server sets `stream_requested` on the heartbeat while somebody is watching,
 * and clears it when the last viewer leaves, so a session cannot be left
 * running by a closed browser tab.
 *
 * Starting is deliberately driven from the heartbeat rather than a command:
 * commands are claimed once and would be lost if the app restarted mid-session,
 * whereas a flag is simply true again on the next beat.
 */
import { NativeModules } from 'react-native';
import type { CloudCredentials } from './CloudFileService';

const { ScreenStreamModule } = NativeModules;

class ScreenStreamServiceImpl {
  /** What we last asked the native side to do, to avoid redundant calls. */
  private wanted = false;

  async sync(c: CloudCredentials, requested: boolean): Promise<void> {
    if (!ScreenStreamModule?.start) return;
    if (requested === this.wanted) {
      // Already in the right state as far as we know. The native loop stops
      // itself when the server says nobody is watching, so re-check that
      // rather than trusting this flag alone.
      if (!requested) return;
      try {
        if (await ScreenStreamModule.isStreaming()) return;
      } catch {
        /* fall through and try to start */
      }
    }
    this.wanted = requested;

    try {
      if (requested) {
        await ScreenStreamModule.start({
          cloudUrl: c.cloudUrl,
          deviceId: c.deviceId,
          apiKey: c.apiKey,
          // Modest defaults: enough to follow what a pupil is doing without
          // making the kiosk itself stutter or eating the school's uplink.
          fps: 6,
          maxWidth: 900,
          quality: 55,
        });
        console.log('[ScreenStream] Live view started');
      } else {
        await ScreenStreamModule.stop();
        console.log('[ScreenStream] Live view stopped');
      }
    } catch (error) {
      console.warn('[ScreenStream] Could not change state:', error);
      // Let the next heartbeat try again.
      this.wanted = !requested;
    }
  }
}

export const ScreenStreamService = new ScreenStreamServiceImpl();
