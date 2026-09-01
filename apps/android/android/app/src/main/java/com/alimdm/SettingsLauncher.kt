package com.alimdm

import android.app.ActivityManager
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext

/**
 * Opens a system settings screen from inside the kiosk.
 *
 * Lock task refuses to start another app, and refuses it *silently*:
 * startActivity returns normally and nothing appears. Every button that sent an
 * operator to Android's settings therefore did nothing at all on a device in
 * kiosk mode — which is every device this runs on. "Display over other apps"
 * and "Usage access" in the post-enrolment wizard were both dead this way.
 *
 * So: drop out of lock task, launch, and let the kiosk screen put it back when
 * the app comes forward again. If the launch fails, re-lock immediately rather
 * than leaving a tablet unlocked because a settings screen was missing.
 */
object SettingsLauncher {

    private const val TAG = "SettingsLauncher"

    /** Time for the platform to finish leaving lock task before the launch. */
    private const val UNLOCK_SETTLE_MS = 300L

    /**
     * @param intents tried in order; the first that resolves is launched. Lets a
     *   caller deep-link to its own entry in a list screen and fall back to the
     *   list itself, rather than sending someone hunting through every app.
     */
    fun launch(context: ReactApplicationContext, promise: Promise?, vararg intents: Intent) {
        val intent = intents.firstOrNull { it.resolveActivity(context.packageManager) != null }
            ?: intents.lastOrNull()
        if (intent == null) {
            promise?.reject("NO_INTENT", "No settings screen to open")
            return
        }
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

        val activity = context.currentActivity
        val am = context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        val locked = am.lockTaskModeState != ActivityManager.LOCK_TASK_MODE_NONE

        if (!locked || activity == null) {
            try {
                context.startActivity(intent)
                promise?.resolve(true)
            } catch (e: Exception) {
                Log.e(TAG, "Could not open settings: ${e.message}")
                promise?.reject("ERROR", "Could not open settings: ${e.message}")
            }
            return
        }

        activity.runOnUiThread {
            try {
                activity.stopLockTask()
                Handler(Looper.getMainLooper()).postDelayed({
                    try {
                        context.startActivity(intent)
                    } catch (e: Exception) {
                        Log.e(TAG, "Launch failed after leaving lock task: ${e.message}")
                        try { activity.startLockTask() } catch (_: Exception) {}
                    }
                }, UNLOCK_SETTLE_MS)
                promise?.resolve(true)
            } catch (e: Exception) {
                Log.e(TAG, "Could not leave lock task: ${e.message}")
                promise?.reject("ERROR", "Could not leave kiosk mode: ${e.message}")
            }
        }
    }
}
