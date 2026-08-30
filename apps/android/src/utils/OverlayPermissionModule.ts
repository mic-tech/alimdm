import { NativeModules } from 'react-native';

interface OverlayPermissionModuleType {
  canDrawOverlays(): Promise<boolean>;
  requestOverlayPermission(): Promise<boolean>;
}

const OverlayPermissionModule: OverlayPermissionModuleType =
  NativeModules.OverlayPermissionModule;

export default OverlayPermissionModule;
