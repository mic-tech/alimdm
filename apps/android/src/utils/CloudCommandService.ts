/**
 * CloudCommandService.ts
 * Consumes the Ali MDM Cloud command queue and the dedicated APK update
 * channel, dispatches actions through ApiService.executeAction (the same path
 * as the local REST/MQTT server), and reports each result back to the server.
 *
 * Channels:
 *   - GET  /api/v1/devices/{id}/commands/  → transient actions (mark 'sent' server-side)
 *   - GET  /api/v1/devices/{id}/updates/   → install_apk (resolves signed download URL)
 *   - POST /api/v1/commands/{id}/result/   → report success/error
 *
 * Triggered from CloudSyncService on each heartbeat when pending_commands > 0.
 *
 * Background-mode caveat: this runs on the JS thread, which React Native freezes
 * while the activity is backgrounded (e.g. External App mode behind a launched
 * app). Polling therefore only runs reliably in WebView/media mode. Full
 * coverage would require a native poller (cf. OverlayService for the screensaver).
 */
import { NativeModules } from 'react-native';
import RNFS from 'react-native-fs';

import { ApiService, ActionResult } from './ApiService';
import { getCloudCredentials, CloudCredentials } from './secureStorage';
import { StorageService } from './storage';
import DiagnosticsModule from './DiagnosticsModule';
import ManagedAppInstaller from './ManagedAppInstaller';
import KioskModule from './KioskModule';
import { CloudFileService } from './CloudFileService';

const { HttpServerModule } = NativeModules;

/** Our own package, so a self-update is recognised as a process-killing command. */
const OWN_PACKAGE = 'com.alimdm';

/** Give up on a result the server never accepted rather than retry it forever. */
const MAX_REPORT_ATTEMPTS = 10;

// Declared as type aliases, not interfaces, so they stay assignable to the
// Record<string, unknown> the storage layer persists.
type PendingReport = {
  commandId: string;
  status: 'success' | 'error';
  result: Record<string, unknown> | null;
  errorMessage: string;
  attempts: number;
};

/** A command persisted before dispatch, so it survives the process dying mid-execution. */
type InflightCommand = {
  commandId: string;
  type: string;
  /** True when the process is expected to die (reboot, self-update): that is the success case. */
  killsProcess: boolean;
};

interface CloudCommand {
  id: string;
  type: string;
  params: Record<string, any>;
  created_at: string;
  expires_at: string;
}

interface CloudUpdate {
  command_id: string;
  package_name: string | null;
  version_name: string | null;
  download_url: string;
  /** Present when the package is a split set; base first. */
  split_urls?: string[];
}

type CommandMapper = (params: Record<string, any>) => {
  command: string;
  params: Record<string, any>;
};

/**
 * Maps a cloud CommandType (snake_case) to the local action name + param shape
 * understood by ApiService.executeAction. URL / brightness / volume are NOT here:
 * those are managed settings delivered through the config-sync channel, not
 * transient commands. install_apk is handled via the /updates/ channel, not here.
 */
const COMMAND_MAP: Record<string, CommandMapper> = {
  screen_on: () => ({ command: 'screenOn', params: {} }),
  screen_off: () => ({ command: 'screenOff', params: {} }),
  reload: () => ({ command: 'reload', params: {} }),
  screensaver_on: () => ({ command: 'screensaverOn', params: {} }),
  screensaver_off: () => ({ command: 'screensaverOff', params: {} }),
  wake: () => ({ command: 'wake', params: {} }),
  speak: (p) => ({ command: 'tts', params: { text: p.text } }),
  play_sound: (p) => ({ command: 'playSound', params: { url: p.url } }),
  audio_stop: () => ({ command: 'audioStop', params: {} }),
  reboot: () => ({ command: 'reboot', params: {} }),
  clear_cache: () => ({ command: 'clearCache', params: {} }),
  toast: (p) => ({ command: 'toast', params: { text: p.message } }),
  execute_js: (p) => ({ command: 'executeJs', params: { code: p.code } }),
  launch_app: (p) => ({ command: 'launchApp', params: { package: p.package_name } }),
  // Kiosk lock / unlock: toggle Android Lock Task (the OS-level kiosk that prevents
  // leaving the app). `lock` re-applies the whitelist lock task; `unlock` releases it.
  // These call KioskModule directly (not the executeAction map) because they need the
  // native Device Owner lock-task API, which has no executeAction callback.
  lock: () => ({ command: 'lockKiosk', params: {} }),
  unlock: () => ({ command: 'unlockKiosk', params: {} }),
  // Surrender Device Owner. One-way: nothing on the device can grant it back,
  // so re-provisioning needs physical ADB access (or a factory reset). Handled
  // below rather than through the map's native path, like lock/unlock.
  release_device_owner: () => ({ command: 'releaseDeviceOwner', params: {} }),
  // NOTE: `screenshot` is handled separately (capture + upload), not via this map.
};

class CloudCommandServiceClass {
  private isPolling = false;
  // Guards against re-dispatching the same command if two polls overlap. The
  // server already only returns 'pending' commands, so this is belt-and-braces.
  private processed = new Set<string>();

  /**
   * Fetch + execute any pending commands and APK updates. Safe to call on every
   * heartbeat; no-ops if a poll is already in flight.
   */
  async poll(creds?: CloudCredentials): Promise<void> {
    if (this.isPolling) return;
    const c = creds ?? (await getCloudCredentials());
    if (!c) return;

    this.isPolling = true;
    try {
      await this.settleOutstanding(c);
      // APK updates first — these are the slowest, and ordering doesn't matter.
      await this.pollUpdates(c);
      await this.pollCommands(c);
    } catch (error) {
      console.error('[CloudCommand] Poll error:', error);
    } finally {
      this.isPolling = false;
      // Keep the dedup set from growing unbounded.
      if (this.processed.size > 200) this.processed.clear();
    }
  }

  // ─── Generic command queue ───────────────────────────────────────────────────

  private async pollCommands(c: CloudCredentials): Promise<void> {
    const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/commands/`, {
      method: 'GET',
      headers: { Authorization: `Bearer ${c.apiKey}` },
    });
    if (!res.ok) return;

    const data = await res.json();
    const commands: CloudCommand[] = data?.commands ?? [];

    for (const cmd of commands) {
      if (this.processed.has(cmd.id)) continue;
      this.processed.add(cmd.id);

      // Skip already-expired commands (don't bother executing/reporting).
      if (cmd.expires_at && new Date(cmd.expires_at).getTime() < Date.now()) {
        continue;
      }

      // Persist before dispatch: `reboot` takes the device down and a self-update
      // replaces this very process, so without a marker on disk their result could
      // never be reported and the command stayed 'sent' on the server for good.
      await StorageService.saveInflightCommand({
        commandId: cmd.id,
        type: cmd.type,
        killsProcess: cmd.type === 'reboot' || cmd.type === 'restart_app',
      } satisfies InflightCommand);

      const outcome = await this.runCommand(cmd, c);
      await StorageService.saveInflightCommand(null);
      await this.reportResult(c, cmd.id, outcome);
    }
  }

  /**
   * Finish anything a previous run could not: a command interrupted by a restart,
   * then results the network refused. Called on every poll and once at startup.
   *
   * The startup call is the one that matters for an interrupted command: the server
   * moved it to 'sent' when it handed it over, so it no longer counts as pending and
   * a heartbeat alone would never trigger a poll to pick the marker back up.
   */
  async settleOutstanding(creds?: CloudCredentials): Promise<void> {
    const c = creds ?? (await getCloudCredentials());
    if (!c) return;
    try {
      await this.reportInflightCommand(c);
      await this.flushPendingReports(c);
    } catch (error) {
      console.error('[CloudCommand] Settle error:', error);
    }
  }

  /**
   * Report a command that was still in flight when the process died. A command
   * expected to kill the process (reboot, self-update) reached its goal, so it is
   * reported as a success; anything else was interrupted and says so honestly.
   */
  private async reportInflightCommand(c: CloudCredentials): Promise<void> {
    const raw = await StorageService.getInflightCommand();
    if (!raw) return;
    await StorageService.saveInflightCommand(null);

    const inflight = raw as InflightCommand;
    if (!inflight.commandId) return;
    this.processed.add(inflight.commandId);

    await this.reportResult(
      c,
      inflight.commandId,
      inflight.killsProcess
        ? { ok: true, result: { restarted: true } }
        : { ok: false, error: 'Device restarted before the command result could be reported' },
    );
  }

  /** Retry results the network refused earlier. Dropped after MAX_REPORT_ATTEMPTS. */
  private async flushPendingReports(c: CloudCredentials): Promise<void> {
    const queue = (await StorageService.getPendingReports()) as PendingReport[];
    if (queue.length === 0) return;

    const stillPending: PendingReport[] = [];
    for (const report of queue) {
      const sent = await this.postResult(c, report);
      if (!sent && report.attempts + 1 < MAX_REPORT_ATTEMPTS) {
        stillPending.push({ ...report, attempts: report.attempts + 1 });
      }
    }
    await StorageService.savePendingReports(stillPending);
  }

  private async enqueueReport(report: PendingReport): Promise<void> {
    const queue = (await StorageService.getPendingReports()) as PendingReport[];
    // Guard against the same result piling up if a poll overlaps a retry.
    const deduped = queue.filter(r => r.commandId !== report.commandId);
    deduped.push(report);
    await StorageService.savePendingReports(deduped);
  }

  private async runCommand(cmd: CloudCommand, c: CloudCredentials): Promise<ActionResult> {
    // Screenshot needs a capture + upload round-trip, not a fire-and-forget action.
    if (cmd.type === 'screenshot') {
      return this.captureAndUploadScreenshot(c);
    }

    // What the tablet has been doing, answered into the command result. The
    // console has never been able to see this: every diagnosis so far has meant
    // having the tablet in hand.
    if (cmd.type === 'get_logs') {
      try {
        const d = await DiagnosticsModule.collect();
        return { ok: true, result: d as unknown as Record<string, unknown> };
      } catch (e: any) {
        return { ok: false, error: e?.message || 'Could not collect diagnostics' };
      }
    }

    // Restart the app itself. Not in COMMAND_MAP: it calls the native module
    // directly, and it is the one command whose own process does not outlive
    // it — the inflight marker above is what reports it, on the way back up.
    if (cmd.type === 'restart_app') {
      try {
        await KioskModule.restartApp();
        return { ok: true, result: { restarting: true } };
      } catch (e: any) {
        return { ok: false, error: e?.message || 'Could not restart the app' };
      }
    }

    // The console cannot reach the tablet directly, so browsing its inbox is a
    // command that answers by uploading the listing rather than a live query.
    if (cmd.type === 'list_inbox') {
      return this.uploadInboxListing(c);
    }
    if (cmd.type === 'delete_inbox_file') {
      const name = String((cmd.params || {}).name ?? '');
      if (!name) return { ok: false, error: 'No file name given' };
      // Folder-qualified: the same track name can sit in several folders once a
      // folder upload has landed, and deleting the wrong one is silent.
      const relPath = String((cmd.params || {}).rel_path ?? '');
      const removed = await CloudFileService.remove(name, relPath);
      // Re-listing straight away keeps the console honest about what is left,
      // instead of showing a file the operator has just deleted.
      await this.uploadInboxListing(c);
      return removed
        ? { ok: true, result: { deleted: name } }
        : { ok: false, error: `Could not delete ${name}` };
    }

    const mapper = COMMAND_MAP[cmd.type];
    if (!mapper) {
      return { ok: false, error: `Unsupported command type: ${cmd.type}` };
    }
    try {
      const { command, params } = mapper(cmd.params || {});

      // Kiosk lock / unlock: toggle the native Lock Task directly. These are not in
      // ApiService.executeAction (no JS callback for them) — they need the Device Owner
      // lock-task API, so call KioskModule here and skip the native/executeAction path.
      if (command === 'lockKiosk' || command === 'unlockKiosk') {
        if (!KioskModule) return { ok: false, error: 'KioskModule unavailable' };
        const locking = command === 'lockKiosk';
        // Persist the setting, not just the lock task. Releasing lock task on
        // its own lasted seconds: KioskScreen re-applies whatever the stored
        // Lock Mode setting says whenever it reloads — on focus, on a config
        // push, on resume — so an unlocked tablet locked itself again while the
        // console reported success and the device's own Security tab still read
        // "on". The setting is what an operator is asking to change.
        //
        // A device-level override: the group's policy wins again the next time
        // it is applied, which is what "Re-apply policy" is for.
        await StorageService.saveKioskEnabled(locking);
        const ok = locking
          ? await KioskModule.startLockTask(null, true, true, true, true)
          : await KioskModule.stopLockTask();
        return ok ? { ok: true } : { ok: false, error: (locking ? 'startLockTask' : 'stopLockTask') + ' failed' };
      }

      // Releasing Device Owner: leave lock task first, mirroring the on-device
      // flow. Clearing Device Owner while still pinned would strand the tablet
      // in a kiosk with no privileged app left that could release it.
      if (command === 'releaseDeviceOwner') {
        if (!KioskModule) return { ok: false, error: 'KioskModule unavailable' };
        try {
          await KioskModule.stopLockTask();
        } catch {
          // Not pinned, or already released — not a reason to abort.
        }
        const ok = await KioskModule.removeDeviceOwner();
        return ok ? { ok: true } : { ok: false, error: 'removeDeviceOwner failed' };
      }

      // Prefer the native handler: it runs whatever the JS thread is doing, which is
      // what the local REST API has always done. Only commands the native layer cannot
      // finish on its own fall through to the JS path below.
      const native = await this.tryNative(command, params);
      if (native) return native;
      return await ApiService.executeAction(command, params);
    } catch (error: any) {
      return { ok: false, error: error?.message ?? String(error) };
    }
  }

  /**
   * Dispatch through the native command handler. Resolves null when the command needs
   * the JS thread (or the native build predates this bridge), so the caller falls back.
   */
  private async tryNative(
    command: string,
    params: Record<string, any>,
  ): Promise<ActionResult | null> {
    if (!HttpServerModule?.executeNativeCommand) return null;
    try {
      const raw: string = await HttpServerModule.executeNativeCommand(
        command,
        JSON.stringify(params ?? {}),
      );
      const parsed = raw ? JSON.parse(raw) : null;
      if (!parsed || parsed.nativelyHandled === false) return null;
      return { ok: true, result: parsed };
    } catch {
      // Never let a native failure swallow the command: let the JS path have its turn.
      return null;
    }
  }

  /**
   * Capture the current screen (reusing the same native capture as the local
   * REST `/api/screenshot`) and upload it to the cloud screenshot endpoint.
   */
  private async captureAndUploadScreenshot(c: CloudCredentials): Promise<ActionResult> {
    if (!HttpServerModule?.captureScreenshotBase64) {
      return { ok: false, error: 'Screenshot capture unavailable on this build' };
    }

    let tmpPath: string | null = null;
    try {
      const base64: string = await HttpServerModule.captureScreenshotBase64();
      if (!base64) return { ok: false, error: 'Empty screenshot' };

      // RN multipart file uploads need a file URI, so stage the PNG on disk.
      tmpPath = `${RNFS.CachesDirectoryPath}/cloud-screenshot-${Date.now()}.png`;
      await RNFS.writeFile(tmpPath, base64, 'base64');

      const form = new FormData();
      form.append('image', {
        uri: `file://${tmpPath}`,
        type: 'image/png',
        name: 'screenshot.png',
      } as any);
      form.append('captured_at', new Date().toISOString());

      const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/screenshot/`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${c.apiKey}` },
        body: form,
      });
      if (!res.ok) {
        return { ok: false, error: `Screenshot upload failed: HTTP ${res.status}` };
      }
      return { ok: true, result: { uploaded: true } };
    } catch (error: any) {
      return { ok: false, error: error?.message ?? String(error) };
    } finally {
      if (tmpPath) {
        RNFS.unlink(tmpPath).catch(() => {/* best-effort cleanup */});
      }
    }
  }

  // ─── APK update channel (install_apk) ────────────────────────────────────────

  private async pollUpdates(c: CloudCredentials): Promise<void> {
    const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/updates/`, {
      method: 'GET',
      headers: { Authorization: `Bearer ${c.apiKey}` },
    });
    if (!res.ok) return;

    const updates: CloudUpdate[] = (await res.json()) ?? [];

    for (const u of updates) {
      if (!u.command_id || this.processed.has(u.command_id)) continue;
      this.processed.add(u.command_id);

      // A self-update replaces this process mid-install, so the marker is what
      // lets the result be reported once the new version comes up.
      await StorageService.saveInflightCommand({
        commandId: u.command_id,
        type: 'install_apk',
        killsProcess: u.package_name === OWN_PACKAGE,
      } satisfies InflightCommand);

      try {
        // split_urls means the server unpacked a .xapk/.apks: base plus config
        // splits, which Android takes as one session or not at all. Anything
        // without it is a plain APK and takes the path it always did.
        const splits: string[] | undefined = u.split_urls;
        const result = splits?.length
          ? await ManagedAppInstaller.installSplitsFromUrls(
              splits,
              c.apiKey, // the download endpoint is authenticated with the device API key
              u.package_name ?? null,
            )
          : await ManagedAppInstaller.installFromUrl(
              u.download_url,
              c.apiKey,
              u.package_name ?? null,
            );
        await StorageService.saveInflightCommand(null);
        await this.reportResult(c, u.command_id, {
          ok: true,
          result: {
            installed_package: u.package_name ?? '',
            version: u.version_name ?? '',
            install_status: result?.status ?? 'success',
          },
        });
      } catch (error: any) {
        await StorageService.saveInflightCommand(null);
        await this.reportResult(c, u.command_id, {
          ok: false,
          error: error?.message ?? String(error),
        });
      }
    }
  }

  /**
   * Send the tablet's current inbox contents to the console.
   *
   * The listing is cached server-side rather than requested live: a tablet that
   * is asleep or off the network still has a last-known state worth showing,
   * with the time it was taken.
   */
  private async uploadInboxListing(c: CloudCredentials): Promise<ActionResult> {
    try {
      const entries = await CloudFileService.list();
      const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/inbox`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${c.apiKey}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ entries }),
      });
      if (!res.ok) return { ok: false, error: `Listing upload failed: HTTP ${res.status}` };
      return { ok: true, result: { files: entries.length } };
    } catch (error: any) {
      return { ok: false, error: error?.message ?? String(error) };
    }
  }

  // ─── Result reporting ────────────────────────────────────────────────────────

  private async reportResult(
    c: CloudCredentials,
    commandId: string,
    outcome: ActionResult,
  ): Promise<void> {
    const report: PendingReport = {
      commandId,
      status: outcome.ok ? 'success' : 'error',
      result: outcome.result ?? null,
      errorMessage: outcome.ok ? '' : outcome.error ?? 'Unknown error',
      attempts: 0,
    };
    const sent = await this.postResult(c, report);
    if (!sent) {
      // The command did run. Queue the result so the dashboard eventually learns
      // its outcome instead of showing it as 'sent' with no answer for ever.
      await this.enqueueReport(report);
    }
  }

  /** POST one result. Returns whether the server accepted it. */
  private async postResult(c: CloudCredentials, report: PendingReport): Promise<boolean> {
    try {
      const res = await fetch(`${c.cloudUrl}/api/v1/commands/${report.commandId}/result/`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${c.apiKey}`,
        },
        body: JSON.stringify({
          status: report.status,
          result: report.result,
          error_message: report.errorMessage,
        }),
      });
      // A 4xx means the server will never accept this result (expired, unknown id),
      // so treat it as done rather than retrying it until the attempt cap.
      if (!res.ok && res.status >= 500) return false;
      return true;
    } catch (error) {
      console.error('[CloudCommand] Failed to report result for', report.commandId, error);
      return false;
    }
  }
}

export const CloudCommandService = new CloudCommandServiceClass();
