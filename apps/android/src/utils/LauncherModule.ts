import { NativeModules } from 'react-native';

interface LauncherModuleType {
  enableHomeLauncher(): Promise<boolean>;
  disableHomeLauncher(): Promise<boolean>;
  isHomeLauncherEnabled(): Promise<boolean>;
  openDefaultLauncherSettings(): Promise<boolean>;
}

const LauncherModule: LauncherModuleType = NativeModules.LauncherModule;

export default LauncherModule;
