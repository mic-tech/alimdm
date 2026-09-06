import { NativeModules, NativeEventEmitter } from 'react-native';

export interface AppInfo {
  packageName: string;
  appName: string;
}

/**
 * One row of the console's inventory. Unlike the two listings above, this is
 * unfiltered: system packages are flagged rather than left out, because the
 * package an operator is hunting for is usually one nobody thought to include.
 */
export interface InstalledAppEntry {
  package_name: string;
  label: string;
  system: boolean;
  updated_system_app: boolean;
  enabled: boolean;
  has_launcher: boolean;
  version_name?: string;
  version_code?: number;
  updated_at?: number;
}

/** Extended app info including non-UI packages (services, VPNs, etc.) */
export interface AppInfoAll extends AppInfo {
  hasLauncherActivity: boolean;
}

interface IAppLauncherModule {
  launchExternalApp(packageName: string): Promise<boolean>;
  isAppInstalled(packageName: string): Promise<boolean>;
  getInstalledApps(): Promise<AppInfo[]>;
  /** Returns all installed apps including non-UI user packages (fixes #112) */
  getAllInstalledApps(): Promise<AppInfoAll[]>;
  /** Everything installed, unfiltered, for the console's inventory. */
  getAppInventory(): Promise<InstalledAppEntry[]>;
  getPackageLabel(packageName: string): Promise<string>;
  getAppIcon(packageName: string, size: number): Promise<string>;
  launchBootApps(): Promise<number>;
  startBackgroundMonitor(): Promise<boolean>;
  stopBackgroundMonitor(): Promise<boolean>;
}

const { AppLauncherModule } = NativeModules;

if (!AppLauncherModule) {
  console.error('[AppLauncherModule] Native module not found. Did you rebuild the app?');
}

export const appLauncherEmitter = new NativeEventEmitter(AppLauncherModule);
export default AppLauncherModule as IAppLauncherModule;
