package com.alimdm

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.BatteryManager
import android.os.Build
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableMap
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Self-update (OTA) for Ali MDM itself.
 *
 * Distinct from [ManagedAppInstallerModule], which installs *other* apps, and
 * from [UpdateModule], which pulls GitHub releases and is compiled out unless
 * ENABLE_SELF_UPDATE is set. This module exists because replacing the running
 * package is not a normal install:
 *
 * Android kills com.alimdm the moment the session commits, so the result
 * broadcast [ManagedAppInstallerModule] relies on **never arrives**. There is no
 * "install succeeded" callback to wait for — the success path *is* the process
 * dying. Anything that needs to survive that cannot live in JS state or in
 * AsyncStorage (whose writes need a running JS thread to flush).
 *
 * So intent is recorded in SharedPreferences with a synchronous commit() before
 * the session is committed, and [reconcile] compares the installed versionCode
 * against that record on the next launch to decide success or failure. A crash
 * at any point leaves a durable record that the next launch reports, rather than
 * a rollout stuck forever in "installing".
 */
class AgentUpdateModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName(): String = "AgentUpdate"

    companion object {
        private const val PREFS = "alimdm_agent_update"
        private const val KEY_TARGET = "pending_target_version_code"
        private const val KEY_FROM = "pending_from_version_code"
        private const val KEY_AT = "pending_started_at"
        private const val INSTALL_ACTION = "com.alimdm.AGENT_UPDATE_RESULT"

        // Anything smaller is an error page, not an APK.
        private const val MIN_APK_BYTES = 50_000L
        // Refuse to replace ourselves on a nearly flat battery: losing power
        // partway through leaves the device with no management agent at all.
        private const val MIN_BATTERY_PCT = 20
    }

    private fun prefs() =
        reactApplicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun currentVersionCode(): Long {
        val ctx = reactApplicationContext
        val info = ctx.packageManager.getPackageInfo(ctx.packageName, 0)
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.longVersionCode
        } else {
            @Suppress("DEPRECATION")
            info.versionCode.toLong()
        }
    }

    /** Current build, so JS can decide whether an offered update is worth taking. */
    @ReactMethod
    fun getVersionInfo(promise: Promise) {
        try {
            val ctx = reactApplicationContext
            val info = ctx.packageManager.getPackageInfo(ctx.packageName, 0)
            promise.resolve(
                Arguments.createMap().apply {
                    putDouble("versionCode", currentVersionCode().toDouble())
                    putString("versionName", info.versionName ?: "")
                    putString("packageName", ctx.packageName)
                },
            )
        } catch (e: Exception) {
            promise.reject("VERSION_FAILED", e.message, e)
        }
    }

    /**
     * Resolve the outcome of an update that was in flight when the process died.
     *
     * Returns null when no install was pending. Otherwise reports success if the
     * running build is now at or beyond the target, and failure if we came back
     * up on the old version (install rejected, or it crashed before applying).
     * The record is cleared either way so one attempt is reported exactly once.
     */
    @ReactMethod
    fun reconcile(promise: Promise) {
        try {
            val p = prefs()
            val target = p.getLong(KEY_TARGET, -1L)
            if (target < 0) {
                promise.resolve(null)
                return
            }
            val from = p.getLong(KEY_FROM, -1L)
            val now = currentVersionCode()
            p.edit().remove(KEY_TARGET).remove(KEY_FROM).remove(KEY_AT).commit()

            val result: WritableMap = Arguments.createMap().apply {
                putDouble("targetVersionCode", target.toDouble())
                putDouble("currentVersionCode", now.toDouble())
            }
            if (now >= target) {
                result.putString("status", "success")
            } else {
                result.putString("status", "failed")
                result.putString(
                    "error",
                    "Restarted on versionCode $now after attempting $target (was $from).",
                )
            }
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("RECONCILE_FAILED", e.message, e)
        }
    }

    /** True when a previous attempt is still unresolved (should reconcile first). */
    @ReactMethod
    fun hasPending(promise: Promise) {
        promise.resolve(prefs().getLong(KEY_TARGET, -1L) >= 0)
    }

    /**
     * Whether conditions allow replacing the app right now.
     *
     * Kept separate from [downloadAndInstall] so callers can distinguish a
     * *deferral* from a *failure*. A flat battery is a "try again later", but if
     * it surfaced as a failed attempt it would burn the rollout's retry budget
     * and the operator would have to push the update again by hand.
     */
    @ReactMethod
    fun canInstallNow(promise: Promise) {
        val result = Arguments.createMap()
        try {
            requireHealthyBattery()
            result.putBoolean("ok", true)
            result.putString("reason", "")
        } catch (e: Exception) {
            result.putBoolean("ok", false)
            result.putString("reason", e.message ?: "not ready")
        }
        promise.resolve(result)
    }

    /**
     * Download, verify and install a new build of this app.
     *
     * The promise resolves when the install has been *committed*, not when it
     * succeeded — by design, since a successful commit kills this process before
     * any result could be delivered. JS must treat resolution as "attempt
     * started" and rely on [reconcile] after the restart for the real outcome.
     */
    @ReactMethod
    fun downloadAndInstall(url: String, sha256: String, targetVersionCode: Double, promise: Promise) {
        Thread {
            var apk: File? = null
            try {
                requireHealthyBattery()
                val target = targetVersionCode.toLong()
                if (target <= currentVersionCode()) {
                    throw RuntimeException(
                        "Refusing to install versionCode $target over ${currentVersionCode()}: not an upgrade.",
                    )
                }
                apk = download(url)
                verifyDigest(apk, sha256)
                verifyIsUpgradeOfSelf(apk, target)
                // Record intent *before* committing, with a synchronous write:
                // once the session commits we may be killed at any instant.
                prefs().edit()
                    .putLong(KEY_TARGET, target)
                    .putLong(KEY_FROM, currentVersionCode())
                    .putLong(KEY_AT, System.currentTimeMillis())
                    .commit()
                commitInstall(apk)
                promise.resolve(
                    Arguments.createMap().apply {
                        putString("status", "committed")
                        putDouble("targetVersionCode", target.toDouble())
                    },
                )
            } catch (e: Exception) {
                // Never leave a pending marker behind for an attempt that never
                // reached commit, or the next launch would report a false failure.
                prefs().edit().remove(KEY_TARGET).remove(KEY_FROM).remove(KEY_AT).commit()
                apk?.delete()
                promise.reject("AGENT_UPDATE_FAILED", e.message, e)
            }
        }.start()
    }

    private fun requireHealthyBattery() {
        val bm = reactApplicationContext
            .getSystemService(Context.BATTERY_SERVICE) as? BatteryManager ?: return
        val level = bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
        val charging = bm.isCharging
        if (!charging && level in 0 until MIN_BATTERY_PCT) {
            throw RuntimeException("Battery $level% and not charging; deferring agent update.")
        }
    }

    private fun download(downloadUrl: String): File {
        val parsed = URL(downloadUrl)
        // Same trust model as ManagedAppInstallerModule: HTTPS in general, but
        // plain HTTP to a private/LAN host so a self-hosted server on a school
        // network works without a public certificate.
        val host = parsed.host
        val isPrivateHost = host.equals("localhost", ignoreCase = true) ||
            host.equals("127.0.0.1", ignoreCase = true) ||
            host.startsWith("10.") || host.startsWith("192.168.") ||
            host.startsWith("172.16.") || host.startsWith("172.17.") ||
            host.startsWith("172.18.") || host.startsWith("172.19.") ||
            host.startsWith("172.2") || host.startsWith("172.30.") ||
            host.startsWith("172.31.") || host.endsWith(".local")
        if (!parsed.protocol.equals("https", ignoreCase = true) && !isPrivateHost) {
            throw RuntimeException("Refusing non-HTTPS agent update URL (scheme: ${parsed.protocol}).")
        }
        val conn = (parsed.openConnection() as HttpURLConnection).apply {
            connectTimeout = 30_000
            readTimeout = 120_000
            requestMethod = "GET"
            setRequestProperty("User-Agent", "AliMDM-AgentUpdater")
        }
        try {
            if (conn.responseCode !in 200..299) {
                throw RuntimeException("Agent update download failed: HTTP ${conn.responseCode}")
            }
            // Staged in cacheDir: if we are killed mid-install the OS can reclaim
            // it, and a partial file is never mistaken for a valid build because
            // the digest is checked before anything else looks at it.
            val out = File(reactApplicationContext.cacheDir, "alimdm-agent-update.apk")
            out.delete()
            conn.inputStream.use { input -> out.outputStream().use { input.copyTo(it) } }
            if (out.length() < MIN_APK_BYTES) {
                out.delete()
                throw RuntimeException("Downloaded file too small (${out.length()} bytes); not an APK.")
            }
            return out
        } finally {
            conn.disconnect()
        }
    }

    private fun verifyDigest(apk: File, expected: String) {
        if (expected.isBlank()) throw RuntimeException("Missing sha256 for agent update.")
        val digest = MessageDigest.getInstance("SHA-256")
        apk.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n <= 0) break
                digest.update(buf, 0, n)
            }
        }
        val actual = digest.digest().joinToString("") { "%02x".format(it) }
        if (!actual.equals(expected, ignoreCase = true)) {
            throw RuntimeException("Agent update digest mismatch (expected $expected, got $actual).")
        }
    }

    /**
     * Reject anything that is not genuinely a newer build of *this* package.
     * A signature mismatch would be refused by the platform anyway, but failing
     * here keeps a bad rollout from burning an attempt and from leaving a
     * pending marker that reads as a crash.
     */
    private fun verifyIsUpgradeOfSelf(apk: File, target: Long) {
        val ctx = reactApplicationContext
        val pm = ctx.packageManager
        val info = pm.getPackageArchiveInfo(apk.absolutePath, 0)
            ?: throw RuntimeException("Agent update is not a valid APK (cannot parse manifest).")
        if (info.packageName != ctx.packageName) {
            throw RuntimeException(
                "Agent update package mismatch: got ${info.packageName}, expected ${ctx.packageName}.",
            )
        }
        val apkVersion = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.longVersionCode
        } else {
            @Suppress("DEPRECATION")
            info.versionCode.toLong()
        }
        if (apkVersion != target) {
            throw RuntimeException(
                "Agent update versionCode mismatch: APK is $apkVersion, rollout expected $target.",
            )
        }
        if (!signaturesMatchSelf(apk)) {
            throw RuntimeException("Agent update is signed with a different key; refusing to install.")
        }
    }

    @Suppress("DEPRECATION")
    private fun signaturesMatchSelf(apk: File): Boolean {
        val ctx = reactApplicationContext
        val pm = ctx.packageManager
        return try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                val mine = pm.getPackageInfo(ctx.packageName, PackageManager.GET_SIGNING_CERTIFICATES)
                    .signingInfo?.apkContentsSigners ?: return false
                val theirs = pm.getPackageArchiveInfo(
                    apk.absolutePath, PackageManager.GET_SIGNING_CERTIFICATES,
                )?.signingInfo?.apkContentsSigners ?: return false
                val mineSet = mine.map { it.toCharsString() }.toSet()
                theirs.isNotEmpty() && theirs.all { mineSet.contains(it.toCharsString()) }
            } else {
                val mine = pm.getPackageInfo(ctx.packageName, PackageManager.GET_SIGNATURES)
                    .signatures ?: return false
                val theirs = pm.getPackageArchiveInfo(
                    apk.absolutePath, PackageManager.GET_SIGNATURES,
                )?.signatures ?: return false
                val mineSet = mine.map { it.toCharsString() }.toSet()
                theirs.isNotEmpty() && theirs.all { mineSet.contains(it.toCharsString()) }
            }
        } catch (_: Exception) {
            false
        }
    }

    /**
     * Commit the replace. Everything after [PackageInstaller.Session.commit] is
     * best-effort: on success this process is killed before it runs. The receiver
     * is registered only so a *failed* install (which does not kill us) still
     * clears the pending marker promptly instead of waiting for a restart.
     */
    private fun commitInstall(apk: File) {
        val ctx = reactApplicationContext
        val installer = ctx.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
        params.setAppPackageName(ctx.packageName)
        val sessionId = installer.createSession(params)
        installer.openSession(sessionId).use { session ->
            session.openWrite("package", 0, apk.length()).use { output ->
                apk.inputStream().use { input -> input.copyTo(output) }
                session.fsync(output)
            }
            val intent = Intent("$INSTALL_ACTION.$sessionId").setPackage(ctx.packageName)
            val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                PendingIntent.FLAG_MUTABLE
            } else {
                0
            }
            val pending = PendingIntent.getBroadcast(ctx, sessionId, intent, flags)
            session.commit(pending.intentSender)
        }
    }

    @ReactMethod
    fun addListener(eventName: String) {
        // Required for RN built-in EventEmitter calls.
    }

    @ReactMethod
    fun removeListeners(count: Int) {
        // Required for RN built-in EventEmitter calls.
    }
}
