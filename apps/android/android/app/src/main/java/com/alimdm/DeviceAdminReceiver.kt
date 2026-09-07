package com.alimdm

import android.app.admin.DeviceAdminReceiver
import android.app.admin.DevicePolicyManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.PersistableBundle

/**
 * Device admin receiver. Besides the standard admin lifecycle, this is where a
 * device provisioned as Device Owner via the setup-wizard QR receives the
 * enrollment token: the cloud packs the token, cloud url, org, and the group
 * and label the operator chose into the provisioning admin-extras bundle, and
 * we persist it so the JS layer can auto-enroll on first launch (see
 * KioskModule.getPendingCloudEnrollment and KioskScreen startup).
 */
class DeviceAdminReceiver : DeviceAdminReceiver() {

    companion object {
        // Shared with KioskModule.getPendingCloudEnrollment().
        const val PREFS = "AliMdmCloudEnrollment"
        const val KEY_HAS_PENDING = "has_pending"
        const val KEY_TOKEN = "enroll_token"
        const val KEY_CLOUD_URL = "cloud_url"
        const val KEY_ORG_ID = "org_id"
        const val KEY_GROUP_ID = "group_id"
        /** Label to give this tablet in the console, set at provisioning time. */
        const val KEY_DEVICE_LABEL = "device_label"

        /**
         * Persist the enrolment the QR packed into the provisioning admin-extras
         * bundle, so the JS layer can auto-enrol on first launch.
         *
         * Shared, because Android has two provisioning completion paths and both
         * carry the same bundle: PROFILE_PROVISIONING_COMPLETE to the receiver
         * below on older releases, ADMIN_POLICY_COMPLIANCE to
         * PolicyComplianceActivity on Android 11 and up. They were written out
         * separately and drifted — the activity stored the token, cloud url and
         * org but not the group or the label, so a tablet provisioned on a
         * modern Android arrived unnamed in the default group however the code
         * was generated, and the console had nothing to say it had happened.
         * One implementation cannot drift from itself.
         *
         * Returns false when the bundle carries no token, which is every
         * provisioning that did not come from one of our QRs.
         */
        fun persistPendingEnrollment(context: Context, extras: PersistableBundle?, via: String): Boolean {
            val token = extras?.getString(KEY_TOKEN)
            if (extras == null || token.isNullOrBlank()) return false

            val group = extras.getString(KEY_GROUP_ID) ?: ""
            val label = extras.getString(KEY_DEVICE_LABEL)?.take(64) ?: ""
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
                .putBoolean(KEY_HAS_PENDING, true)
                .putString(KEY_TOKEN, token)
                .putString(KEY_CLOUD_URL, extras.getString(KEY_CLOUD_URL) ?: "")
                .putString(KEY_ORG_ID, extras.getString(KEY_ORG_ID) ?: "")
                .putString(KEY_GROUP_ID, group)
                .putString(KEY_DEVICE_LABEL, label)
                // Synchronous: provisioning may hand straight over to the app.
                .commit()

            // Which path staged it, and what the QR carried. Never the token.
            // A tablet that arrived in the wrong group left no evidence at all
            // of whether the extras were missing from the code or dropped on
            // the way, and by the time anyone noticed, logcat had rolled over.
            android.util.Log.i(
                "AliMDM-Provision",
                "Staged enrolment via $via: group=${group.ifBlank { "(none)" }}, " +
                    "label=${label.ifBlank { "(none)" }}"
            )
            return true
        }

        /** The admin-extras bundle out of a provisioning intent, if it has one. */
        fun provisioningExtras(intent: Intent): PersistableBundle? =
            intent.getParcelableExtra(DevicePolicyManager.EXTRA_PROVISIONING_ADMIN_EXTRAS_BUNDLE)

        /**
         * Pin AliMDM as the persistent Home launcher (Device Owner only).
         * Called during provisioning so the "choose launcher" prompt never shows:
         * by the time the device reaches the home screen, AliMDM is already the
         * locked default. No-op if not Device Owner. The JS/config layer may also
         * toggle this later via KioskModule.setDefaultLauncherMode.
         */
        fun pinHomeLauncher(context: Context) {
            try {
                val dpm = context.getSystemService(Context.DEVICE_POLICY_SERVICE) as DevicePolicyManager
                if (!dpm.isDeviceOwnerApp(context.packageName)) return
                val admin = ComponentName(context, DeviceAdminReceiver::class.java)
                val filter = IntentFilter(Intent.ACTION_MAIN).apply {
                    addCategory(Intent.CATEGORY_HOME)
                    addCategory(Intent.CATEGORY_DEFAULT)
                }
                dpm.addPersistentPreferredActivity(
                    admin, filter, ComponentName(context, MainActivity::class.java)
                )
            } catch (_: Exception) {}
        }
    }

    override fun onEnabled(context: Context, intent: Intent) {
        super.onEnabled(context, intent)
    }

    override fun onDisabled(context: Context, intent: Intent) {
        super.onDisabled(context, intent)
    }

    override fun onProfileProvisioningComplete(context: Context, intent: Intent) {
        super.onProfileProvisioningComplete(context, intent)
        persistPendingEnrollment(context, provisioningExtras(intent), "PROFILE_PROVISIONING_COMPLETE")
        // Older Android provisioning path. Pin the launcher now so the picker
        // never appears when the device reaches home.
        pinHomeLauncher(context)
    }
}
