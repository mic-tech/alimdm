package com.alimdm

import android.app.ActivityManager
import android.content.Context
import android.os.Build
import android.provider.Settings
import com.facebook.react.bridge.*

/**
 * What a tablet can tell the console about itself when something is wrong.
 *
 * The console can see that a device is online and what it was asked to do, and
 * nothing about why any of it failed. Every diagnosis so far has needed the
 * tablet in hand. This collects the two things that actually answer the
 * question: the app's own recent log, and the state of the permissions and
 * modes that decide how it behaves.
 */
class DiagnosticsModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName() = "DiagnosticsModule"

    companion object {
        /** Roughly what a command result can carry without being unwieldy. */
        private const val MAX_LOGCAT_BYTES = 96 * 1024
        private const val LOGCAT_LINES = 400
    }

    /**
     * A snapshot for the console: the app's own buffer, this process's logcat,
     * and the state that shapes its behaviour.
     */
    @ReactMethod
    fun collect(promise: Promise) {
        try {
            val out = Arguments.createMap()
            out.putString("app_log", DebugLog.recent())
            out.putString("logcat", readOwnLogcat())
            out.putMap("state", deviceState())
            out.putString("collected_at", java.text.SimpleDateFormat(
                "yyyy-MM-dd'T'HH:mm:ss'Z'", java.util.Locale.US).apply {
                timeZone = java.util.TimeZone.getTimeZone("UTC")
            }.format(java.util.Date()))
            promise.resolve(out)
        } catch (e: Exception) {
            promise.reject("ERROR", "Could not collect diagnostics: ${e.message}")
        }
    }

    /**
     * An app may read its own logcat and nothing else, which is exactly the
     * scope wanted here: our native modules log through android.util.Log
     * directly and those lines are not in [DebugLog]'s buffer.
     */
    private fun readOwnLogcat(): String {
        return try {
            val pid = android.os.Process.myPid().toString()
            val process = ProcessBuilder(
                "logcat", "-d", "-v", "time", "-t", LOGCAT_LINES.toString(), "--pid=$pid",
            ).redirectErrorStream(true).start()
            val text = process.inputStream.bufferedReader().use { it.readText() }
            process.waitFor()
            if (text.length > MAX_LOGCAT_BYTES) {
                "…trimmed to the last ${MAX_LOGCAT_BYTES / 1024}KB…\n" +
                    text.substring(text.length - MAX_LOGCAT_BYTES)
            } else {
                text
            }
        } catch (e: Exception) {
            "logcat unavailable: ${e.message}"
        }
    }

    /** The handful of facts that explain most of what a tablet does. */
    private fun deviceState(): WritableMap {
        val m = Arguments.createMap()
        m.putString("android", Build.VERSION.RELEASE)
        m.putString("model", Build.MODEL)
        m.putInt("app_version_code", BuildConfig.VERSION_CODE)

        val dpm = reactContext.getSystemService(Context.DEVICE_POLICY_SERVICE)
            as android.app.admin.DevicePolicyManager
        m.putBoolean("device_owner", dpm.isDeviceOwnerApp(reactContext.packageName))

        // Free space, because an install that fails for want of it fails with no
        // explanation an operator can see, and the first guess is always wrong
        // without a number to check it against.
        try {
            val stat = android.os.StatFs(android.os.Environment.getDataDirectory().absolutePath)
            m.putDouble("storage_free_bytes", stat.availableBytes.toDouble())
            m.putDouble("storage_total_bytes", stat.totalBytes.toDouble())
        } catch (e: Exception) {
            DebugLog.d("Diagnostics", "Cannot read free storage: ${e.message}")
        }

        // Whether the display is awake, which decides whether the next answer
        // means anything.
        val pm = reactContext.getSystemService(Context.POWER_SERVICE) as? android.os.PowerManager
        val screenOn = pm?.isInteractive ?: true
        m.putBoolean("screen_on", screenOn)

        // lockTaskModeState cannot be trusted while the display is off: a tablet
        // that came back locked the instant its screen woke reported NONE twice
        // while it slept. Reported as a plain "no", that reads as a kiosk which
        // has fallen open, and the next hour goes on re-locking a tablet that
        // was never unlocked. Say nothing rather than something false.
        val am = reactContext.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        if (screenOn) {
            m.putBoolean("lock_task", am.lockTaskModeState != ActivityManager.LOCK_TASK_MODE_NONE)
        } else {
            m.putNull("lock_task")
        }

        // The three that are granted outside the app and decide whether screen
        // capture, overlays and foreground detection work at all.
        m.putBoolean("accessibility_running", AliMdmAccessibilityService.isRunning())
        // Taps need this capability, and it comes from the service's XML config,
        // which Android reads when it binds the service. A config change that
        // has not been picked up looks exactly like a tap that goes nowhere, so
        // it is worth stating rather than inferring.
        m.putBoolean("can_perform_gestures", AliMdmAccessibilityService.canPerformGestures())
        m.putBoolean(
            "can_write_secure_settings",
            reactContext.checkSelfPermission(android.Manifest.permission.WRITE_SECURE_SETTINGS)
                == android.content.pm.PackageManager.PERMISSION_GRANTED,
        )
        m.putBoolean("can_draw_overlays", Settings.canDrawOverlays(reactContext))
        m.putBoolean("usage_access", hasUsageAccess())

        // The package verifier, and whether we can do anything about it.
        //
        // An app the fleet built and signed itself is not recognised by Play
        // Protect, so on a newly enrolled tablet the install is refused with
        // INSTALL_FAILED_VERIFICATION_FAILURE and somebody has to walk to the
        // device. Reading these costs nothing and says which of the two knobs
        // is set, which is more than anyone could see before.
        m.putInt("package_verifier_enable", globalInt("package_verifier_enable"))
        m.putInt("verifier_verify_adb_installs", globalInt("verifier_verify_adb_installs"))
        m.putInt("package_verifier_user_consent", secureInt("package_verifier_user_consent"))
        m.putString("verifier_write", VerifierControl.lastAttempt)
        return m
    }

    /** -1 when the setting is absent, which is itself worth reporting. */
    private fun globalInt(key: String): Int =
        try { Settings.Global.getInt(reactContext.contentResolver, key, -1) } catch (_: Exception) { -1 }

    private fun secureInt(key: String): Int =
        try { Settings.Secure.getInt(reactContext.contentResolver, key, -1) } catch (_: Exception) { -1 }

    private fun hasUsageAccess(): Boolean {
        return try {
            val usm = reactContext.getSystemService(Context.USAGE_STATS_SERVICE)
                as? android.app.usage.UsageStatsManager ?: return false
            val now = System.currentTimeMillis()
            // Without the appop this comes back empty rather than throwing.
            !usm.queryUsageStats(
                android.app.usage.UsageStatsManager.INTERVAL_BEST, now - 60_000, now,
            ).isNullOrEmpty()
        } catch (e: Exception) {
            false
        }
    }
}
