import { NativeModules } from 'react-native';

interface IMotionDetectionModule {
  compareImages(imagePath: string, threshold: number): Promise<boolean>;
  reset(): Promise<boolean>;
}

const { MotionDetectionModule } = NativeModules;

if (!MotionDetectionModule) {
  console.error('[MotionDetectionModule] Native module not found. Did you rebuild the app?');
}

export default MotionDetectionModule as IMotionDetectionModule;
