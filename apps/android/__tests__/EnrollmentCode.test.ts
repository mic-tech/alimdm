/**
 * The enrolment codes a tablet can scan. The provisioning QR is the one that
 * matters: it is what the console shows, and on a tablet whose setup wizard
 * lost the token it is the same code scanned a second time.
 */
const { parseEnrollmentCode } = require('../src/utils/CloudSyncService');

describe('parseEnrollmentCode', () => {
  it('reads the console provisioning QR, group and label included', () => {
    const qr = JSON.stringify({
      'android.app.extra.PROVISIONING_DEVICE_ADMIN_COMPONENT_NAME': 'com.alimdm/.DeviceAdminReceiver',
      'android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE': {
        enroll_token: 'fc059dc1',
        cloud_url: 'https://mdm.example.com',
        org_id: '',
        group_id: 'iqra-tabs',
        device_label: 'IQRA Tab 432C',
      },
    });
    expect(parseEnrollmentCode(qr)).toEqual({
      url: 'https://mdm.example.com',
      token: 'fc059dc1',
      groupId: 'iqra-tabs',
      label: 'IQRA Tab 432C',
    });
  });

  it('leaves group and label out when the QR had none', () => {
    const qr = JSON.stringify({
      'android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE': { enroll_token: 't', cloud_url: 'https://c' },
    });
    expect(parseEnrollmentCode(qr)).toEqual({ url: 'https://c', token: 't', groupId: undefined, label: undefined });
  });

  it('still reads the older formats', () => {
    expect(parseEnrollmentCode('{"url":"https://c","token":"t"}')).toEqual({ url: 'https://c', token: 't' });
    expect(parseEnrollmentCode('https://c|t')).toEqual({ url: 'https://c', token: 't' });
    expect(parseEnrollmentCode(' t ')).toEqual({ token: 't' });
    expect(parseEnrollmentCode('   ')).toBeNull();
  });
});
