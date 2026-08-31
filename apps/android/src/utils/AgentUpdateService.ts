import { NativeModules } from 'react-native';

/**
 * On-demand OTA for Ali MDM itself.
 *
 * The server offers an update on the heartbeat; this service drives it and
 * reports the outcome back. The awkward part is that a *successful* install
 * kills this process — so "did it work?" can only be answered after the
 * restart, by AgentUpdateModule.reconcile() comparing the running versionCode
 * against the intent it persisted natively before committing.
 *
 * The resulting contract is deliberately blunt: every heartbeat reconciles
 * first, then considers a new attempt. A device that dies at any point simply
 * reports on its next heartbeat instead of stranding the rollout.
 */

export interface AgentUpdateOffer {
  version_code: number;
  version_name: string;
  sha256: string;
  download_url: string;
  attempt: number;
  max_attempts: number;
}

type ReconcileResult = {
  status: 'success' | 'failed';
  targetVersionCode: number;
  currentVersionCode: number;
  error?: string;
} | null;

interface AgentUpdateNative {
  getVersionInfo(): Promise<{ versionCode: number; versionName: string; packageName: string }>;
  reconcile(): Promise<ReconcileResult>;
  hasPending(): Promise<boolean>;
  canInstallNow(): Promise<{ ok: boolean; reason: string }>;
  downloadAndInstall(url: string, sha256: string, targetVersionCode: number): Promise<{ status: string }>;
}

const Native: AgentUpdateNative | undefined = NativeModules.AgentUpdate;

class AgentUpdateServiceClass {
  /** Guards against a second attempt while a download/install is in flight. */
  private busy = false;

  isAvailable(): boolean {
    return !!Native;
  }

  async getVersionCode(): Promise<number> {
    if (!Native) return 0;
    try {
      return (await Native.getVersionInfo()).versionCode;
    } catch {
      return 0;
    }
  }

  /**
   * Resolve any attempt that was in flight before the last restart.
   * Returns the report to send to the server, or null if there was none.
   */
  async reconcile(): Promise<ReconcileResult> {
    if (!Native) return null;
    try {
      return await Native.reconcile();
    } catch (e) {
      // A module-level failure here must not wedge the rollout: report it as a
      // failed attempt so the server's attempt cap eventually stops retrying.
      return {
        status: 'failed',
        targetVersionCode: 0,
        currentVersionCode: 0,
        error: `reconcile failed: ${String(e)}`,
      };
    }
  }

  /**
   * Whether an install may start right now. A false result is a deferral, not a
   * failure: the caller should skip this heartbeat silently and try again,
   * without spending one of the rollout's attempts.
   */
  async canInstallNow(): Promise<{ ok: boolean; reason: string }> {
    if (!Native) return { ok: false, reason: 'AgentUpdate native module unavailable' };
    try {
      return await Native.canInstallNow();
    } catch (e) {
      return { ok: false, reason: String(e) };
    }
  }

  /**
   * Take an offered update. Resolving means the install was *committed*, not
   * that it succeeded — the process is expected to die moments later. Returns
   * false when the attempt could not be started (and why, via onError).
   */
  async apply(offer: AgentUpdateOffer, onError: (msg: string) => void): Promise<boolean> {
    if (!Native) {
      onError('AgentUpdate native module unavailable');
      return false;
    }
    if (this.busy) return false;
    this.busy = true;
    try {
      const current = await this.getVersionCode();
      if (current >= offer.version_code) {
        // Already up to date — the rollout row simply has not been closed out
        // yet. Deliberately not routed through onError: reporting this as a
        // failure would overwrite a success that already happened.
        return false;
      }
      await Native.downloadAndInstall(offer.download_url, offer.sha256, offer.version_code);
      return true;
    } catch (e: any) {
      onError(e?.message ? String(e.message) : String(e));
      return false;
    } finally {
      this.busy = false;
    }
  }
}

export const AgentUpdateService = new AgentUpdateServiceClass();
export default AgentUpdateService;
