import AsyncStorage from '@react-native-async-storage/async-storage';
import { DeviceEventEmitter, NativeModules } from 'react-native';

import { StorageService, KEYS } from './storage';
import DeviceControlService from '../services/DeviceControlService';
import { CloudCommandService } from './CloudCommandService';
import { CloudFileService } from './CloudFileService';
import { ScreenStreamService } from './ScreenStreamService';
import AgentUpdateService, { AgentUpdateOffer } from './AgentUpdateService';
import { getCapabilities } from './capabilities';
import KioskModule from './KioskModule';
import {
  CloudCredentials,
  SensitiveConfig,
  clearCloudCredentials,
  clearSecureApiKey,
  clearSecureBasicAuthPassword,
  clearSecureMqttPassword,
  clearSecurePin,
  exportSensitiveConfig,
  getCloudCredentials,
  importSensitiveConfig,
  saveCloudCredentials,
} from './secureStorage';

export const CONFIG_UPDATED_EVENT = 'ALIMDM_CONFIG_UPDATED';
/** Fired when the operator changes this tablet's label in the console. */
export const DEVICE_LABEL_UPDATED_EVENT = 'ALIMDM_DEVICE_LABEL_UPDATED';
/** Fired when the cloud connection state changes, so the kiosk can show it. */
export const CLOUD_STATUS_EVENT = 'ALIMDM_CLOUD_STATUS';

/**
 * unmanaged — no cloud enrolment on this device at all.
 * offline   — enrolled, but nothing has reached the server recently (usually Wi-Fi).
 * online    — enrolled and checking in.
 */
export type CloudStatus = 'unmanaged' | 'offline' | 'online';

// Matches the console's own Online/Offline threshold, so the tablet and the
// dashboard never disagree about the same device.
const CLOUD_OFFLINE_AFTER_MS = 3 * 60_000;
export const FORCE_UNENROLL_EVENT = 'ALIMDM_FORCE_UNENROLL';

const HEARTBEAT_INTERVAL_MS = 30_000;

/**
 * Shortest gap between two heartbeats, whichever clock asked for them. Comfortably below
 * HEARTBEAT_INTERVAL_MS so a normal tick is never dropped, and above the few seconds that
 * separate a headless run from the JS timer it wakes up.
 */
const MIN_HEARTBEAT_GAP_MS = 20_000;

interface HeartbeatResponse {
  status: string;
  pending_commands: number;
  /** Files queued for this tablet's inbox. Its own channel, so a stuck command
   *  cannot hold up a worksheet. */
  pending_files?: number;
  /** True while an operator has the tablet's live view open. */
  stream_requested?: boolean;
  server_time: string;
  sync_action: 'none' | 'apply';
  config: Record<string, unknown> | null;
  sensitive_config: SensitiveConfig | null;
  config_version: number;
  force_unenroll: boolean;
  /** Operator-set label for this tablet. Absent on servers older than this field. */
  device_label?: string;
  /** Non-null only while an operator-triggered self-update is outstanding. */
  agent_update?: AgentUpdateOffer | null;
}

/**
 * Stable id for this tablet, sent as `serial_number` at enrolment.
 *
 * Must not be empty: the server falls back to hashing the enrolment token, and
 * because a fleet shares one token every tablet would land on the same device
 * id, each enrolment overwriting the previous one's API key.
 */
export async function getDeviceSerial(): Promise<string> {
  try {
    return (await KioskModule.getDeviceIdentifier()) || '';
  } catch {
    // Let the server assign one rather than blocking enrolment outright.
    return '';
  }
}

async function simpleHash(str: string): Promise<string> {
  try {
    const g = globalThis as any;
    if (g.crypto?.subtle) {
      const buffer = await g.crypto.subtle.digest('SHA-256', new g.TextEncoder().encode(str));
      return Array.from(new Uint8Array(buffer) as Uint8Array)
        .map((b: number) => b.toString(16).padStart(2, '0'))
        .join('');
    }
  } catch {}
  // djb2 fallback
  let h = 5381;
  for (let i = 0; i < str.length; i++) {
    h = (((h << 5) + h) + str.charCodeAt(i)) & 0xffffffff;
  }
  return Math.abs(h).toString(16);
}

class CloudSyncServiceClass {
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private lastHeartbeatAtMs = 0;
  // Distinct from lastHeartbeatAtMs, which is stamped *before* the attempt: only
  // a reply from the server counts as being connected.
  private lastHeartbeatOkAtMs = 0;
  private startedAtMs = Date.now();
  private lastStatus: CloudStatus | null = null;
  private isRunning = false;

  // ─── Lifecycle ───────────────────────────────────────────────────────────────

  async start(): Promise<void> {
    // Treat "running" as running *and* actually ticking. If a previous attempt
    // set the flag but never armed the timer, this guard would otherwise make
    // every later start() a no-op and the device would stay silent for the life
    // of the process — heartbeating is the only thing that keeps it managed, and
    // nothing else would notice.
    if (this.isRunning && this.heartbeatTimer) return;
    const creds = await getCloudCredentials();
    if (!creds) return;
    this.isRunning = true;
    try {
      await this.startInternal(creds);
    } catch (error) {
      // Every await between here and arming the timer can reject — storage,
      // the first heartbeat, a native module that is not ready. Leaving the
      // flag set on the way out is what turns a transient failure into a
      // permanently silent device, so give the next caller a clean slate.
      this.isRunning = false;
      console.error('[CloudSync] start() failed; heartbeat not armed', error);
    }
  }

  private async startInternal(creds: CloudCredentials): Promise<void> {
    // The interval below only runs while the activity is resumed: React Native stops
    // dispatching JS timers on host pause, so behind a launched external app this loop
    // stops and the device is reported offline after two minutes while running perfectly.
    // KioskWatchdogService owns the ticker that covers that case, driving the heartbeat
    // through a headless task (CloudHeartbeatTaskService). Mirror the enrolment for it,
    // since it reads AsyncStorage directly with no bridge available, and start it now:
    // MainActivity's own call already ran before we enrolled.
    await StorageService.saveCloudEnrolled(true);
    KioskModule.ensureKeepAliveWatchdog?.().catch(() => {/* best-effort */});
    // Keep the CPU + WiFi awake so this heartbeat/poll loop survives screen-off; without
    // it the device drops off the cloud and can no longer be woken remotely.
    KioskModule.acquireCloudWakeLock().catch(() => {/* best-effort */});
    // Also exempt from Doze so it holds up on battery-powered devices. Opens a one-time
    // system dialog (there is no silent path, even as Device Owner) which the system
    // suppresses in lock task, and is a no-op once granted or in Play builds. Best-effort:
    // the wake lock is the primary mechanism.
    KioskModule.requestIgnoreBatteryOptimizations().catch(() => {/* best-effort */});
    // Close out whatever the previous process left open (a reboot we executed, a
    // result the network refused) before the first heartbeat. Fire-and-forget: it
    // must never delay the device coming back online.
    CloudCommandService.settleOutstanding(creds).catch(() => {/* handles its own errors */});
    await this.sendHeartbeat(creds);
    this.heartbeatTimer = setInterval(
      () => getCloudCredentials().then(c => c && this.sendHeartbeat(c)),
      HEARTBEAT_INTERVAL_MS,
    );
  }

  stop(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
    KioskModule.releaseCloudWakeLock().catch(() => {/* best-effort */});
    this.isRunning = false;
  }

  // ─── Heartbeat ───────────────────────────────────────────────────────────────

  /**
   * @param options.force bypass the debounce, for an explicit republish (e.g. after the
   *   permission wizard changes the capability list) rather than a scheduled tick.
   */
  async sendHeartbeat(creds?: CloudCredentials, options?: { force?: boolean }): Promise<void> {
    const c = creds ?? await getCloudCredentials();
    if (!c) return;

    // Two clocks drive this: the JS interval in the foreground, and the native ticker
    // (via CloudHeartbeatTaskService) while backgrounded. Starting a headless task also
    // re-arms JS timers, so both fire for a moment and the device heartbeats twice per
    // period. Collapse anything that lands too close to the previous beat.
    const now = Date.now();
    if (!options?.force && now - this.lastHeartbeatAtMs < MIN_HEARTBEAT_GAP_MS) return;
    this.lastHeartbeatAtMs = now;

    try {
      const config = await StorageService.exportConfig();
      const configStr = JSON.stringify(config);

      // Hash-based change detection — avoids patching every save*() method
      const lastHash = await StorageService.getLastSentConfigHash();
      const currentHash = await simpleHash(configStr);

      let configUpdatedAt = await StorageService.getConfigUpdatedAt();
      if (currentHash !== lastHash) {
        configUpdatedAt = new Date().toISOString();
        await StorageService.saveConfigUpdatedAt(configUpdatedAt);
        await StorageService.saveLastSentConfigHash(currentHash);
      }

      const configVersion = await StorageService.getConfigVersion();
      const sensitiveConfig = await exportSensitiveConfig();

      // Collect device telemetry for live status
      let telemetry: Record<string, unknown> = {};
      try {
        const status = await DeviceControlService.getStatus();
        // Refresh the capability list every heartbeat so granting a permission
        // after enrollment is reflected in the dashboard on the next tick.
        const capabilities = await getCapabilities().catch(() => [] as string[]);
        telemetry = {
          battery: {
            level: status.battery.level,
            charging: status.battery.charging,
          },
          network: {
            wifi_ssid: status.wifi.ssid,
            wifi_signal_dbm: status.wifi.signalStrength,
            ip_address: status.device.ip,
          },
          display: {
            screen_on: status.screen.on,
            brightness: status.screen.brightness,
          },
          app: {
            current_url: status.webview.currentUrl,
            kiosk_mode: status.device.kioskMode,
            device_owner: status.device.isDeviceOwner,
            capabilities,
          },
          system: {
            app_version: status.device.version,
            model: status.device.model,
            manufacturer: status.device.manufacturer,
            android_version: status.device.androidVersion,
            free_storage_mb: status.device.freeStorageMb,
            free_memory_mb: status.device.freeMemoryMb,
            uptime_seconds: status.device.uptime,
          },
        };
      } catch {
        // Telemetry is best-effort — never block heartbeat
      }

      const response = await fetch(
        `${c.cloudUrl}/api/v1/devices/${c.deviceId}/heartbeat/`,
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${c.apiKey}`,
          },
          body: JSON.stringify({
            ...telemetry,
            config,
            config_version: configVersion,
            config_updated_at: configUpdatedAt,
            sensitive_config: sensitiveConfig,
          }),
        },
      );

      if (response.status === 401 || response.status === 403) {
        // Device was removed from cloud server-side
        await this._wipeAndUnenroll();
        return;
      }
      if (!response.ok) return;
      // The server answered: this is the one place we know the link works.
      this.lastHeartbeatOkAtMs = Date.now();
      this._publishStatus();

      const data: HeartbeatResponse = await response.json();

      if (data.force_unenroll) {
        await this._wipeAndUnenroll();
        return;
      }

      if (data.sync_action === 'apply' && data.config) {
        await StorageService.importConfig(data.config);
        if (data.sensitive_config) {
          await importSensitiveConfig(data.sensitive_config);
        }
        await StorageService.saveConfigVersion(data.config_version);
        await StorageService.saveConfigUpdatedAt(new Date().toISOString());
        await StorageService.saveLastSentConfigHash(''); // force re-hash next tick
        DeviceEventEmitter.emit(CONFIG_UPDATED_EVENT);
      } else if (data.config_version > configVersion) {
        await StorageService.saveConfigVersion(data.config_version);
      }

      // The label is per-device, so it arrives on every heartbeat rather than in
      // the group config. Only write + notify when it actually changed, so the
      // kiosk screen does not re-render on every tick. `undefined` means the
      // server predates this field — leave whatever is stored alone.
      if (data.device_label !== undefined) {
        const storedLabel = await StorageService.getDeviceLabel();
        if (data.device_label !== storedLabel) {
          await StorageService.saveDeviceLabel(data.device_label);
          DeviceEventEmitter.emit(DEVICE_LABEL_UPDATED_EVENT);
        }
      }

      // Agent (self) OTA. Reconcile unconditionally — an attempt that was in
      // flight when the process was killed can only be resolved here, and it
      // must be reported even on a heartbeat where no new update is offered.
      await this._handleAgentUpdate(c, data.agent_update ?? null);

      // Pull + execute any pending commands / APK updates. Fire-and-forget so
      // the heartbeat loop is never blocked by a long install; the service
      // guards against overlapping polls internally.
      if (data.pending_commands > 0) {
        CloudCommandService.poll(c).catch(() => {/* poll() handles its own errors */});
      }

      // Files ride their own channel: a large slide deck must not delay a
      // reboot command, and a stuck command must not delay a worksheet.
      if ((data.pending_files ?? 0) > 0) {
        CloudFileService.poll(c).catch(() => {/* poll() handles its own errors */});
      }

      // Live view follows the flag in both directions, so closing the console
      // tab stops the tablet capturing without needing a command to arrive.
      ScreenStreamService.sync(c, data.stream_requested === true)
        .catch(() => {/* sync() reports its own errors */});
    } catch (error) {
      console.error('[CloudSync] Heartbeat error:', error);
    }
  }

  /**
   * Drive the agent (self) OTA for one heartbeat.
   *
   * Order matters. Reconciliation runs first and unconditionally: if the last
   * attempt succeeded, this process is a *new build* that has no memory of the
   * attempt beyond what the native module persisted, and the server is still
   * waiting to hear how it went. Only once that is settled do we consider
   * starting a new attempt.
   *
   * A committed install kills us before `apply()` returns, so there is
   * deliberately no success path here — the next launch reports it.
   */
  private async _handleAgentUpdate(
    c: CloudCredentials,
    offer: AgentUpdateOffer | null,
  ): Promise<void> {
    if (!AgentUpdateService.isAvailable()) return;
    try {
      const settled = await AgentUpdateService.reconcile();
      if (settled) {
        await this._reportAgentUpdate(c, settled.status, settled.error ?? '', settled.targetVersionCode);
        // The offer in *this* response was computed before that report landed,
        // so a just-completed update still looks outstanding. Acting on it would
        // start a second attempt, immediately find the device already on the
        // target, and overwrite the success with a failure. The next heartbeat
        // sees the updated status and stops offering.
        if (settled.status === 'success') return;
      }
      if (!offer) return;

      // Already at or past the offer. Two ways to get here: the stale-offer
      // race above, or a build that arrived by a route the server never saw —
      // a sideload over ADB, say. Report it rather than returning quietly: the
      // rollout's goal is met either way, and staying silent leaves the row
      // "queued" for ever, so the console shows a pending update that can never
      // complete and the tablet is re-offered it on every heartbeat.
      const current = await AgentUpdateService.getVersionCode();
      if (current >= offer.version_code) {
        await this._reportAgentUpdate(c, 'success', '', offer.version_code);
        return;
      }

      // Deferrals (flat battery, module unavailable) must not consume an
      // attempt: skip quietly and let the next heartbeat re-offer the update.
      const ready = await AgentUpdateService.canInstallNow();
      if (!ready.ok) {
        console.log('[CloudSync] Deferring agent update:', ready.reason);
        return;
      }

      // Tell the server an attempt is starting *before* starting it. This is
      // what makes the attempt counter trustworthy: if the download or install
      // wedges the device hard enough that it never reports again, the attempt
      // is still on record and the server's cap will retire the rollout.
      await this._reportAgentUpdate(c, 'installing', '', offer.version_code);
      await AgentUpdateService.apply(offer, async (msg) => {
        await this._reportAgentUpdate(c, 'failed', msg, offer.version_code);
      });
    } catch (error) {
      console.error('[CloudSync] Agent update error:', error);
    }
  }

  private async _reportAgentUpdate(
    c: CloudCredentials,
    status: 'installing' | 'success' | 'failed',
    error: string,
    // Which rollout this result belongs to. Without it the server cannot tell a
    // late report from an old attempt apart from one about the current rollout,
    // and a stale success marks a newer, untried rollout complete.
    versionCode: number,
  ): Promise<void> {
    try {
      await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/agent-update/`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${c.apiKey}`,
        },
        body: JSON.stringify({ status, error, version_code: versionCode }),
      });
    } catch {
      // Best-effort: the server re-offers the update on the next heartbeat, and
      // reconcile() is idempotent, so a dropped report costs one extra cycle.
    }
  }

  /**
   * Current cloud connection state, for display on the kiosk itself.
   *
   * A tablet whose Wi-Fi has dropped keeps showing its apps and looks perfectly
   * healthy, so without this the only place the problem is visible is a console
   * nobody in the room is looking at.
   */
  async getCloudStatus(): Promise<CloudStatus> {
    const creds = await getCloudCredentials();
    if (!creds) return 'unmanaged';
    if (this.lastHeartbeatOkAtMs === 0) {
      // Nothing has succeeded yet this process. Stay quiet briefly after launch
      // rather than flashing a warning during normal start-up.
      return Date.now() - this.startedAtMs > CLOUD_OFFLINE_AFTER_MS ? 'offline' : 'online';
    }
    return Date.now() - this.lastHeartbeatOkAtMs > CLOUD_OFFLINE_AFTER_MS ? 'offline' : 'online';
  }

  /** Emit only on change, so the kiosk re-renders once per transition. */
  private _publishStatus(): void {
    this.getCloudStatus().then((status) => {
      if (status === this.lastStatus) return;
      this.lastStatus = status;
      DeviceEventEmitter.emit(CLOUD_STATUS_EVENT, status);
    }).catch(() => {/* display-only */});
  }

  // ─── Enrollment ──────────────────────────────────────────────────────────────

  async enroll(
    cloudUrl: string,
    token: string,
    deviceInfo: Record<string, string>,
    groupId?: string,
  ): Promise<{ success: boolean; error?: string; organizationName?: string }> {
    const url = cloudUrl.replace(/\/$/, '');
    try {
      // Declare what this device can do so the dashboard shows only viable
      // actions from the start (refreshed later on every heartbeat).
      const capabilities = await getCapabilities().catch(() => [] as string[]);
      const response = await fetch(`${url}/api/v1/devices/enroll/`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token, device_info: { ...deviceInfo, capabilities }, group_id: groupId || undefined }),
      });

      const data = await response.json();

      if (!response.ok) {
        return { success: false, error: data.error ?? 'Enrollment failed' };
      }

      // Wipe all local settings before applying cloud config
      await AsyncStorage.multiRemove(Object.values(KEYS));
      await Promise.all([
        clearSecurePin(),
        clearSecureApiKey(),
        clearSecureMqttPassword(),
        clearSecureBasicAuthPassword(),
      ]);

      await saveCloudCredentials({
        deviceId: data.device_id,
        apiKey: data.api_key,
        cloudUrl: url,
        organizationName: data.organization_name,
      });

      // First heartbeat — will pull cloud config if one exists
      await this.start();

      // Local settings were wiped above. Force the running app to reload so the
      // reset (or cloud-pushed) config takes effect immediately. Without this, the
      // kiosk keeps its pre-enrollment config in memory until an app restart
      // (the heartbeat only emits this event when the cloud actually pushes a
      // config, so a freshly-enrolled device with no cloud config never reloaded).
      DeviceEventEmitter.emit(CONFIG_UPDATED_EVENT);

      return { success: true, organizationName: data.organization_name };
    } catch {
      return { success: false, error: 'Cannot reach server' };
    }
  }

  // ─── Zero-touch provisioning ──────────────────────────────────────────────────

  /**
   * Consume an enrollment handed over by Device Owner provisioning (the
   * setup-wizard QR). Runs once on startup: if the device isn't already
   * enrolled and the native layer has a pending token, enroll automatically and
   * clear it. Best-effort and idempotent.
   */
  async consumePendingProvisioningEnrollment(): Promise<void> {
    try {
      if (await this.isEnrolled()) return;
      const pending = await KioskModule.getPendingCloudEnrollment?.();
      if (!pending?.enroll_token || !pending?.cloud_url) return;

      const PC = (NativeModules as any).PlatformConstants;
      const result = await this.enroll(pending.cloud_url, pending.enroll_token, {
        model: PC?.Model ?? '',
        manufacturer: PC?.Manufacturer ?? '',
        android_version: PC?.Release ?? '',
        app_version: PC?.appVersion ?? '',
        serial_number: await getDeviceSerial(),
      }, (pending as any).group_id);
      if (result.success) {
        // A device provisioned via the setup-wizard QR is a Device Owner kiosk:
        // pin Ali MDM as the persistent Home launcher so the "choose launcher"
        // prompt never appears and the user can't switch back to the stock one.
        // Best-effort and DO-only (no-op otherwise); the cloud config can still
        // override this later. Only on provisioning auto-enroll, not manual enroll.
        KioskModule.setDefaultLauncherMode(true).catch(() => {/* DO only */});
      }
      // Clear on success, or on a definitive rejection, so a bad/used token
      // doesn't get retried on every launch. Network errors are left pending.
      if (result.success || result.error !== 'Cannot reach server') {
        await KioskModule.clearPendingCloudEnrollment?.();
      }
    } catch {
      // Never block startup on provisioning.
    }
  }

  // ─── Unenrollment ────────────────────────────────────────────────────────────

  async unenroll(): Promise<void> {
    const creds = await getCloudCredentials();
    if (creds) {
      try {
        await fetch(`${creds.cloudUrl}/api/v1/devices/${creds.deviceId}/unenroll/`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${creds.apiKey}` },
        });
      } catch {} // best-effort, proceed regardless
    }
    await this._wipeAndUnenroll();
  }

  private async _wipeAndUnenroll(): Promise<void> {
    this.stop();

    // Clear all AsyncStorage settings
    await AsyncStorage.multiRemove(Object.values(KEYS));

    // Clear all Keychain secrets
    await Promise.all([
      clearSecurePin(),
      clearSecureApiKey(),
      clearSecureMqttPassword(),
      clearSecureBasicAuthPassword(),
      clearCloudCredentials(),
    ]);

    DeviceEventEmitter.emit(FORCE_UNENROLL_EVENT);
  }

  // ─── Helpers ─────────────────────────────────────────────────────────────────

  async getCredentials(): Promise<CloudCredentials | null> {
    return getCloudCredentials();
  }

  async isEnrolled(): Promise<boolean> {
    return (await getCloudCredentials()) !== null;
  }
}

export const CloudSyncService = new CloudSyncServiceClass();
