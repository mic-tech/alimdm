/**
 * ManagedAppInstaller.ts
 * TS bridge for the native ManagedAppInstaller module — installs a third-party
 * APK pushed by Ali MDM Cloud (OTA). Silent install requires Device Owner.
 */
import { NativeModules } from 'react-native';

const { ManagedAppInstaller: Native } = NativeModules;

export interface ManagedInstallResult {
  status: string;
  package: string;
}

export const ManagedAppInstaller = {
  /** Whether silent install is possible (Device Owner mode). */
  async isDeviceOwner(): Promise<boolean> {
    if (!Native?.isDeviceOwner) return false;
    try {
      return await Native.isDeviceOwner();
    } catch {
      return false;
    }
  },

  /**
   * Download an APK from `url` (with optional Bearer `authToken`) and install it
   * silently. Rejects if not Device Owner or on download/install failure.
   */
  installFromUrl(
    url: string,
    authToken: string | null,
    expectedPackage: string | null,
  ): Promise<ManagedInstallResult> {
    if (!Native?.installFromUrl) {
      return Promise.reject(new Error('ManagedAppInstaller native module unavailable'));
    }
    return Native.installFromUrl(url, authToken, expectedPackage);
  },

  /**
   * Install a split package: a base APK and the config splits that attach to it.
   *
   * The parts go into one install session and are committed together — a split
   * cannot be installed on its own, so there is no sequence of single installs
   * that adds up to this. `urls` arrives base first.
   */
  installSplitsFromUrls(
    urls: string[],
    authToken: string | null,
    expectedPackage: string | null,
  ): Promise<ManagedInstallResult> {
    if (!Native?.installSplitsFromUrls) {
      return Promise.reject(
        new Error('This build cannot install split packages; update Ali MDM first.'),
      );
    }
    return Native.installSplitsFromUrls(urls, authToken, expectedPackage);
  },
};

export default ManagedAppInstaller;
