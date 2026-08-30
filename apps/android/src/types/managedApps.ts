/**
 * Ali MDM - Managed Apps Types
 * 
 * Unified data model for features:
 * - #66: Accessibility whitelist for other apps
 * - #67: Multi-app mode (multiple apps on home screen)
 * - #37: Background apps (launch on boot, keep alive)
 */

export interface ManagedApp {
  /** Android package name, e.g. "com.example.app" */
  packageName: string;

  /** Human-readable display name (cached from PackageManager) */
  displayName: string;

  // --- #67: Multi-App Mode ---
  /** Show this app on the kiosk home screen grid */
  showOnHomeScreen: boolean;

  // --- #37: Background Apps ---
  /** Launch this app in the background on device boot */
  launchOnBoot: boolean;
  /** Monitor and restart this app if it closes/crashes */
  keepAlive: boolean;

  // --- #66: Accessibility Whitelist ---
  /** Allow this app's accessibility services (DevicePolicyManager.setPermittedAccessibilityServices) */
  allowAccessibility: boolean;
}

/**
 * Create a new ManagedApp with sensible defaults.
 * By default, the app is shown on the home screen.
 */
export function createManagedApp(
  packageName: string,
  displayName: string,
  overrides?: Partial<ManagedApp>,
): ManagedApp {
  return {
    packageName,
    displayName,
    showOnHomeScreen: true,
    launchOnBoot: false,
    keepAlive: false,
    allowAccessibility: false,
    ...overrides,
  };
}

/**
 * Validate a package name format
 */
export function isValidPackageName(pkg: string): boolean {
  return /^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$/.test(pkg);
}
