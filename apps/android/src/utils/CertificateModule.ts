import { NativeModules } from 'react-native';

export interface CertificateInfo {
  fingerprint: string;
  url: string;
  expiryTime: number;
  expiryDate: string;
  isExpired: boolean;
}

interface ICertificateModule {
  clearAcceptedCertificates(): Promise<boolean>;
  getAcceptedCertificates(): Promise<CertificateInfo[]>;
  removeCertificate(fingerprint: string): Promise<boolean>;
}

const { CertificateModule } = NativeModules;

export default CertificateModule as ICertificateModule;
