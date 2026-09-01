import { NativeModules, Platform } from 'react-native';

export interface DeviceState {
  android?: string;
  model?: string;
  app_version_code?: number;
  device_owner?: boolean;
  lock_task?: boolean;
  accessibility_running?: boolean;
  can_write_secure_settings?: boolean;
  can_draw_overlays?: boolean;
  usage_access?: boolean;
}

export interface Diagnostics {
  app_log: string;
  logcat: string;
  state: DeviceState;
  collected_at: string;
}

interface IDiagnosticsModule {
  /** The app's recent log, this process's logcat, and the state that shapes both. */
  collect(): Promise<Diagnostics>;
}

const DiagnosticsModule: IDiagnosticsModule =
  Platform.OS === 'android'
    ? NativeModules.DiagnosticsModule
    : { collect: () => Promise.reject(new Error('Android only')) };

export default DiagnosticsModule;
