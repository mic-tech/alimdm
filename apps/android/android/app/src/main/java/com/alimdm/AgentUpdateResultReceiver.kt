package com.alimdm

// Why Android refused to replace us.
//
// A self-update kills its own process the moment the session commits, so a
// receiver registered at runtime dies with it and PackageInstaller's result has
// nowhere to land. Everything the agent could say afterwards was "I came back on
// the old version" — true, and useless. A tablet that refused three updates in a
// row said the same sentence three times and never once said why, and working it
// out took a USB cable and an evening.
//
// A receiver declared in the manifest is delivered to a fresh process instead,
// so the reason survives the death of the one that asked for it. It is written
// where reconcile() will find it on the next start.

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller

class AgentUpdateResultReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        val status = intent.getIntExtra(
            PackageInstaller.EXTRA_STATUS,
            PackageInstaller.STATUS_FAILURE,
        )
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: ""

        // STATUS_PENDING_USER_ACTION means Android wants somebody to tap Install.
        // On a Device Owner tablet in a locked kiosk there is nobody to tap it,
        // and saying so beats reporting a bare failure.
        val label = when (status) {
            PackageInstaller.STATUS_SUCCESS -> "success"
            PackageInstaller.STATUS_PENDING_USER_ACTION ->
                "Android asked for the install to be confirmed on the device, " +
                    "which cannot happen on a locked tablet"
            else -> describe(status) + (if (message.isNotEmpty()) ": $message" else "")
        }
        DebugLog.i(TAG, "Agent install result: status=$status ${message.ifEmpty { "(no message)" }}")

        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putInt(KEY_RESULT_STATUS, status)
            .putString(KEY_RESULT_MESSAGE, label)
            .commit()
    }

    /** Android's numeric statuses, in words an operator can act on. */
    private fun describe(status: Int): String = when (status) {
        PackageInstaller.STATUS_FAILURE_BLOCKED ->
            "Blocked — something on the device refused the install, often Play Protect or a device policy"
        PackageInstaller.STATUS_FAILURE_CONFLICT ->
            "Conflict — the installed app disagrees with this one, usually a different signing key"
        PackageInstaller.STATUS_FAILURE_INCOMPATIBLE ->
            "Incompatible — this build does not run on this device"
        PackageInstaller.STATUS_FAILURE_INVALID ->
            "Invalid — the APK was rejected as malformed"
        PackageInstaller.STATUS_FAILURE_STORAGE ->
            "Not enough storage to install"
        PackageInstaller.STATUS_FAILURE_ABORTED ->
            "Aborted before it finished"
        PackageInstaller.STATUS_FAILURE_TIMEOUT ->
            "Timed out"
        else -> "Install failed (status $status)"
    }

    companion object {
        private const val TAG = "AgentUpdateResult"

        // Shared with AgentUpdateModule, which reads them in reconcile().
        const val PREFS = "alimdm_agent_update"
        const val KEY_RESULT_STATUS = "last_result_status"
        const val KEY_RESULT_MESSAGE = "last_result_message"
    }
}
