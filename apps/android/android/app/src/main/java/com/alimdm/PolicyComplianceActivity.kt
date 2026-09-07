package com.alimdm

import android.app.Activity
import android.os.Bundle

/**
 * Handles ACTION_ADMIN_POLICY_COMPLIANCE. REQUIRED for Device Owner provisioning
 * on Android 12+ (API 31): the system launches this as the final provisioning
 * step and the device is not provisioned until it returns RESULT_OK.
 *
 * This is also where, on modern Android, we receive the admin extras bundle the
 * cloud packed into the QR — token, cloud url, org, and the group and label the
 * operator chose when generating the code. Persisting it is DeviceAdminReceiver's
 * job, shared with the older PROFILE_PROVISIONING_COMPLETE path so the two
 * cannot store different things.
 */
class PolicyComplianceActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Shared with the receiver's older path on purpose: this one used to
        // keep its own copy that stored the token but not the group or the
        // label, which is how a QR-provisioned tablet arrived unnamed in the
        // default group. See DeviceAdminReceiver.persistPendingEnrollment.
        DeviceAdminReceiver.persistPendingEnrollment(
            this, DeviceAdminReceiver.provisioningExtras(intent), "ADMIN_POLICY_COMPLIANCE"
        )
        // Pin AliMDM as Home now, while still inside provisioning, so the
        // "choose launcher" picker never appears once the device reaches home.
        DeviceAdminReceiver.pinHomeLauncher(this)
        setResult(RESULT_OK)
        finish()
    }
}
