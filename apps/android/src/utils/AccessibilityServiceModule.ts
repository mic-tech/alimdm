import { NativeModules } from 'react-native';

interface AccessibilityServiceModuleType {
  isServiceEnabled(): Promise<boolean>;
  openAccessibilitySettings(): Promise<boolean>;
}

const AccessibilityServiceModule: AccessibilityServiceModuleType =
  NativeModules.AccessibilityServiceModule;

export default AccessibilityServiceModule;
