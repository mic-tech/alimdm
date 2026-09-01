/**
 * The post-enrolment prompt is the only moment anyone is asked for the
 * permissions the app cannot grant itself, so what decides whether it appears
 * has to be right in both directions: it must not be raised in front of a class
 * for nothing, and it must not stay silent on a device that needs it.
 *
 * The accessibility row is the one that matters. It used to report itself
 * satisfied for any Device Owner without checking, which is exactly the fleet
 * where the service was off.
 */
// The wizard is Android-only and short-circuits elsewhere; the react-native
// jest preset reports ios.
jest.mock('react-native/Libraries/Utilities/Platform', () => ({
  __esModule: true,
  default: {
    OS: 'android',
    select: (spec: Record<string, unknown>) => spec.android ?? spec.default,
  },
}));

// export {} makes this a module: without it the file shares a global scope with
// the other test file, and both declaring NativeModules is a type error.
export {};

const { NativeModules } = require('react-native');

const accessibility = {
  isAccessibilityServiceEnabled: jest.fn(),
  isAccessibilityServiceRunning: jest.fn(),
  openAccessibilitySettings: jest.fn(() => Promise.resolve(true)),
  enableViaDeviceOwner: jest.fn(() => Promise.resolve(true)),
  setPermittedAccessibilityPackages: jest.fn(() => Promise.resolve(true)),
};
const kiosk = {
  isDeviceOwner: jest.fn(() => Promise.resolve(true)),
  hasUsageStatsPermission: jest.fn(() => Promise.resolve(true)),
  requestUsageStatsPermission: jest.fn(() => Promise.resolve(true)),
  isIgnoringBatteryOptimizations: jest.fn(() => Promise.resolve(true)),
  requestIgnoreBatteryOptimizations: jest.fn(() => Promise.resolve(true)),
  openAndroidSettings: jest.fn(() => Promise.resolve(true)),
};
const overlay = {
  canDrawOverlays: jest.fn(() => Promise.resolve(true)),
  requestOverlayPermission: jest.fn(() => Promise.resolve(true)),
};
NativeModules.AccessibilityModule = accessibility;
NativeModules.KioskModule = kiosk;
NativeModules.OverlayPermissionModule = overlay;

const { PermissionsAndroid } = require('react-native');
PermissionsAndroid.check = jest.fn(() => Promise.resolve(true));

const { hasOutstandingPermissions } = require('../src/components/PermissionWizard');

describe('hasOutstandingPermissions', () => {
  beforeEach(() => {
    accessibility.isAccessibilityServiceEnabled.mockResolvedValue(true);
    kiosk.isDeviceOwner.mockResolvedValue(true);
  });

  it('says nothing is outstanding when every permission is in place', async () => {
    await expect(hasOutstandingPermissions()).resolves.toBe(false);
  });

  it('reports the accessibility service on a Device Owner that has it off', async () => {
    // The case that went unnoticed on the whole fleet: Device Owner, everything
    // else granted, and the service that carries screen capture switched off.
    accessibility.isAccessibilityServiceEnabled.mockResolvedValue(false);
    await expect(hasOutstandingPermissions()).resolves.toBe(true);
  });

  it('reports a missing special-access permission', async () => {
    overlay.canDrawOverlays.mockResolvedValueOnce(false);
    await expect(hasOutstandingPermissions()).resolves.toBe(true);
  });
});
