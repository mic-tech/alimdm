import { NativeModules } from 'react-native';

const { UpdateModule } = NativeModules;

/**
 * Play Store compliance flag.
 * When building with `./gradlew bundleRelease -Pplaystore`, this is false
 * and all update methods become no-ops. The UI hides the update section.
 */
export const ENABLE_SELF_UPDATE: boolean = UpdateModule?.ENABLE_SELF_UPDATE ?? true;

interface VersionInfo {
  versionName: string;
  versionCode: number;
}

interface UpdateInfo {
  version: string;
  name: string;
  notes: string;
  publishedAt: string;
  downloadUrl: string;
  isPrerelease?: boolean;
}

export default {
  /**
   * Get current app version (always available, even in Play Store builds)
   */
  getCurrentVersion(): Promise<VersionInfo> {
    return UpdateModule.getCurrentVersion();
  },

  /**
   * Check for available updates on GitHub (stable channel only)
   * No-op in Play Store builds.
   */
  checkForUpdates(): Promise<UpdateInfo> {
    if (!ENABLE_SELF_UPDATE) {
      return Promise.reject(new Error('Self-update disabled in Play Store builds'));
    }
    return UpdateModule.checkForUpdates();
  },

  /**
   * Check for updates with optional beta/pre-release channel support.
   * No-op in Play Store builds.
   * @param includeBeta - If true, includes pre-release versions
   */
  checkForUpdatesWithChannel(includeBeta: boolean): Promise<UpdateInfo> {
    if (!ENABLE_SELF_UPDATE) {
      return Promise.reject(new Error('Self-update disabled in Play Store builds'));
    }
    return UpdateModule.checkForUpdatesWithChannel(includeBeta);
  },

  /**
   * Check if the app has permission to install APKs from unknown sources.
   * No-op in Play Store builds.
   */
  checkInstallPermission(): Promise<boolean> {
    if (!ENABLE_SELF_UPDATE) {
      return Promise.resolve(false);
    }
    return UpdateModule.checkInstallPermission();
  },

  /**
   * Open the system settings page to allow installing from unknown sources.
   * No-op in Play Store builds.
   */
  openInstallPermissionSettings(): Promise<boolean> {
    if (!ENABLE_SELF_UPDATE) {
      return Promise.resolve(false);
    }
    return UpdateModule.openInstallPermissionSettings();
  },

  /**
   * Download and install an update.
   * No-op in Play Store builds.
   * @param downloadUrl - Direct download URL for the APK
   * @param version - Version string for display
   */
  downloadAndInstall(downloadUrl: string, version: string): Promise<boolean> {
    if (!ENABLE_SELF_UPDATE) {
      return Promise.reject(new Error('Self-update disabled in Play Store builds'));
    }
    return UpdateModule.downloadAndInstall(downloadUrl, version);
  },
};
