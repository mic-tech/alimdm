package com.alimdm

import android.app.PendingIntent
import android.app.admin.DevicePolicyManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageInstaller
import android.os.Build
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.ReadableArray
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Installs a *managed third-party APK* pushed by AliMDM Cloud (OTA).
 *
 * Distinct from [UpdateModule], which self-updates AliMDM from GitHub and is
 * gated by ENABLE_SELF_UPDATE. This module downloads an arbitrary APK over an
 * authenticated URL and installs it **silently via the PackageInstaller session
 * API**, which only succeeds in Device Owner mode (a kiosk cannot show the
 * system install prompt). The silent-install path mirrors UpdateModule.installApk().
 */
class ManagedAppInstallerModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName(): String = "ManagedAppInstaller"

    companion object {
        private const val TAG = "ManagedAppInstaller"
        private const val INSTALL_ACTION = "com.alimdm.MANAGED_APP_INSTALL_RESULT"
        private const val MIN_APK_BYTES = 50_000L // anything smaller is almost certainly an HTML error page

        // A config split can legitimately be a few kilobytes — one language's
        // strings, one density's drawables — so the whole-APK floor above would
        // reject perfectly good parts. This floor only catches an error page.
        private const val MIN_SPLIT_BYTES = 1_000L
    }

    @ReactMethod
    fun isDeviceOwner(promise: Promise) {
        try {
            val dpm = reactApplicationContext
                .getSystemService(Context.DEVICE_POLICY_SERVICE) as DevicePolicyManager
            promise.resolve(dpm.isDeviceOwnerApp(reactApplicationContext.packageName))
        } catch (e: Exception) {
            promise.resolve(false)
        }
    }

    /**
     * Download [downloadUrl] (with an optional `Authorization: Bearer <authToken>`
     * header) and install it silently. Rejects if not Device Owner, on download
     * failure, or on install failure. Resolves with `{status, package}` on success.
     */
    @ReactMethod
    fun installFromUrl(
        downloadUrl: String,
        authToken: String?,
        expectedPackage: String?,
        promise: Promise,
    ) {
        Thread {
            var apkFile: File? = null
            try {
                val dpm = reactApplicationContext
                    .getSystemService(Context.DEVICE_POLICY_SERVICE) as DevicePolicyManager
                if (!dpm.isDeviceOwnerApp(reactApplicationContext.packageName)) {
                    promise.reject(
                        "NOT_DEVICE_OWNER",
                        "Silent install requires Device Owner mode.",
                    )
                    return@Thread
                }
                if (downloadUrl.isEmpty()) {
                    promise.reject("BAD_URL", "Download URL is empty.")
                    return@Thread
                }
                // A silent Device Owner install is a full-privilege operation, so the caller
                // must declare which package it expects; we verify the downloaded APK against
                // it below before committing (defends against a swapped/malicious APK).
                if (expectedPackage.isNullOrEmpty()) {
                    promise.reject("BAD_PACKAGE", "expectedPackage is required for a silent install.")
                    return@Thread
                }

                apkFile = downloadApk(downloadUrl, authToken)
                // Verify the downloaded APK actually is the expected package before install.
                verifyApkPackage(apkFile, expectedPackage)
                // installSilently owns the promise from here (async via receiver).
                installSilently(apkFile, expectedPackage, promise)
            } catch (e: Exception) {
                android.util.Log.e(TAG, "Install error: ${e.message}", e)
                apkFile?.delete()
                promise.reject("INSTALL_ERROR", e.message ?: "Unknown install error", e)
            }
        }.start()
    }

    /**
     * Download a split package and install it.
     *
     * Splits are not installable one at a time. A split APK carries no code of
     * its own and Android rejects it without the base it attaches to, so the
     * whole set goes into a single PackageInstaller session and is committed
     * once: all of it lands, or none of it does. Installing them in sequence —
     * the obvious reading of "several APKs" — fails on the first split.
     *
     * urls arrive base first, in the order the server laid the set out.
     */
    @ReactMethod
    fun installSplitsFromUrls(
        urls: ReadableArray,
        authToken: String?,
        expectedPackage: String?,
        promise: Promise,
    ) {
        Thread {
            val files = mutableListOf<File>()
            try {
                val dpm = reactApplicationContext
                    .getSystemService(Context.DEVICE_POLICY_SERVICE) as DevicePolicyManager
                if (!dpm.isDeviceOwnerApp(reactApplicationContext.packageName)) {
                    promise.reject("NOT_DEVICE_OWNER", "Silent install requires Device Owner mode.")
                    return@Thread
                }
                if (urls.size() == 0) {
                    promise.reject("BAD_URL", "No APKs to install.")
                    return@Thread
                }
                if (expectedPackage.isNullOrEmpty()) {
                    promise.reject("BAD_PACKAGE", "expectedPackage is required for a silent install.")
                    return@Thread
                }

                for (i in 0 until urls.size()) {
                    val url = urls.getString(i) ?: continue
                    // The first is the base and must look like a whole APK; the
                    // rest may legitimately be tiny. Only the first clears the
                    // cache directory, or each download would delete the last.
                    files.add(
                        downloadApk(
                            url, authToken,
                            fileName = "part-$i.apk",
                            minBytes = if (i == 0) MIN_APK_BYTES else MIN_SPLIT_BYTES,
                            clean = i == 0,
                        ),
                    )
                }
                verifySplitPackage(files, expectedPackage)
                installSplitsSilently(files, expectedPackage, promise)
            } catch (e: Exception) {
                DebugLog.errorProduction(TAG, "Split install error: ${e.message}")
                files.forEach { it.delete() }
                promise.reject("INSTALL_ERROR", e.message ?: "Unknown install error", e)
            }
        }.start()
    }

    /**
     * Check a downloaded set really is the package that was asked for.
     *
     * Only some parts answer: a config split may carry no manifest the package
     * manager will parse, and returns null. That is normal and not a reason to
     * refuse. What must never happen is a part naming a *different* package —
     * that is a swapped download, and it would be installed with Device Owner
     * privileges — so every part that does answer has to agree, and at least
     * one has to answer at all.
     */
    private fun verifySplitPackage(files: List<File>, expectedPackage: String) {
        val pm = reactApplicationContext.packageManager
        var identified = 0
        for (f in files) {
            val info = pm.getPackageArchiveInfo(f.absolutePath, 0) ?: continue
            if (info.packageName != expectedPackage) {
                throw RuntimeException(
                    "APK package mismatch: ${f.name} is ${info.packageName}, expected $expectedPackage.",
                )
            }
            identified++
        }
        if (identified == 0) {
            throw RuntimeException("No part of the download is a readable APK for $expectedPackage.")
        }
    }

    /** Write every part into one session and commit it once. */
    private fun installSplitsSilently(files: List<File>, expectedPackage: String, promise: Promise) {
        val context = reactApplicationContext
        val packageInstaller = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
        params.setAppPackageName(expectedPackage)
        // Tell Android how big the whole set is before writing any of it. A
        // split package can be enormous — one seen here was 771MB across four
        // parts — and without a size the installer only discovers a tablet is
        // short of space part-way through writing, after the whole download.
        // With it, a tablet that cannot fit the app says so at once.
        params.setSize(files.sumOf { it.length() })

        val sessionId = packageInstaller.createSession(params)
        val session = packageInstaller.openSession(sessionId)
        files.forEachIndexed { i, f ->
            // The name is the session's own label for the part, not a file name
            // Android reads anything into — it only has to be unique.
            session.openWrite("part-$i", 0, f.length()).use { output ->
                f.inputStream().use { input -> input.copyTo(output) }
                session.fsync(output)
            }
        }
        DebugLog.i(TAG, "Committing a split install of ${files.size} APKs for $expectedPackage")
        finishSession(session, sessionId, files, expectedPackage, promise)
    }

    private fun downloadApk(
        downloadUrl: String,
        authToken: String?,
        fileName: String? = null,
        minBytes: Long = MIN_APK_BYTES,
        clean: Boolean = true,
    ): File {
        // Enforce HTTPS: this APK is installed silently with Device Owner privileges, so a
        // cleartext download (which the app otherwise permits) would let a network MITM swap
        // in a malicious APK. HttpURLConnection does not auto-follow cross-protocol redirects
        // (https -> http), so validating the initial scheme is sufficient; a downgrade redirect
        // surfaces as a non-2xx response and is rejected below.
        val parsed = URL(downloadUrl)
        // Enforce HTTPS for public hosts. For self-hosted deployments on a trusted
        // LAN (e.g. a school's own server), allow HTTP to private/local addresses so
        // the device can pull APKs from an internal server without a public TLS cert.
        val host = parsed.host
        val isPrivateHost = host.equals("localhost", ignoreCase = true) ||
            host.equals("127.0.0.1", ignoreCase = true) ||
            host.startsWith("10.") || host.startsWith("192.168.") ||
            host.startsWith("172.16.") || host.startsWith("172.17.") ||
            host.startsWith("172.18.") || host.startsWith("172.19.") ||
            host.startsWith("172.2") || host.startsWith("172.30.") ||
            host.startsWith("172.31.") || host.endsWith(".local")
        if (!parsed.protocol.equals("https", ignoreCase = true) && !isPrivateHost) {
            throw RuntimeException("Refusing non-HTTPS APK download URL (scheme: ${parsed.protocol}).")
        }
        val conn = (parsed.openConnection() as HttpURLConnection).apply {
            connectTimeout = 30_000
            readTimeout = 120_000
            requestMethod = "GET"
            instanceFollowRedirects = true
            if (!authToken.isNullOrEmpty()) {
                setRequestProperty("Authorization", "Bearer $authToken")
            }
            setRequestProperty("Accept", "application/vnd.android.package-archive")
            setRequestProperty("User-Agent", "AliMDM-ManagedInstaller")
        }

        try {
            conn.connect()
            if (conn.responseCode !in 200..299) {
                throw RuntimeException("Download failed: HTTP ${conn.responseCode}")
            }

            val dir = File(reactApplicationContext.cacheDir, "managed_apks").apply { mkdirs() }
            if (clean) {
                dir.listFiles()?.forEach { it.delete() } // clean up previous downloads
            }
            val outFile = File(dir, fileName ?: "managed-${System.currentTimeMillis()}.apk")

            conn.inputStream.use { input ->
                outFile.outputStream().use { output -> input.copyTo(output) }
            }

            if (outFile.length() < minBytes) {
                val size = outFile.length()
                outFile.delete()
                throw RuntimeException("Downloaded file too small ($size bytes); likely not an APK.")
            }
            return outFile
        } finally {
            conn.disconnect()
        }
    }

    /**
     * Parse the downloaded APK's manifest (without installing it) and reject if its package
     * name does not match [expectedPackage]. Closes the gap where a MITM/malicious download
     * could deliver an APK for a different package than the one the cloud asked to install.
     */
    private fun verifyApkPackage(apkFile: File, expectedPackage: String?) {
        if (expectedPackage.isNullOrEmpty()) {
            throw RuntimeException("expectedPackage is required for a silent install.")
        }
        val pm = reactApplicationContext.packageManager
        val info = pm.getPackageArchiveInfo(apkFile.absolutePath, 0)
            ?: throw RuntimeException("Downloaded file is not a valid APK (cannot parse manifest).")
        if (info.packageName != expectedPackage) {
            throw RuntimeException(
                "APK package mismatch: downloaded ${info.packageName}, expected $expectedPackage.",
            )
        }
    }

    private fun installSilently(apkFile: File, expectedPackage: String?, promise: Promise) {
        val context = reactApplicationContext
        val packageInstaller = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(
            PackageInstaller.SessionParams.MODE_FULL_INSTALL
        )
        if (!expectedPackage.isNullOrEmpty()) {
            params.setAppPackageName(expectedPackage)
        }
        params.setSize(apkFile.length())

        val sessionId = packageInstaller.createSession(params)
        val session = packageInstaller.openSession(sessionId)
        session.openWrite("package", 0, apkFile.length()).use { output ->
            apkFile.inputStream().use { input -> input.copyTo(output) }
            session.fsync(output)
        }

        finishSession(session, sessionId, listOf(apkFile), expectedPackage, promise)
    }

    /**
     * Commit a session and settle the JS promise on its result.
     *
     * Shared by the single-APK and split paths so the two cannot drift: the
     * receiver registration, the mutable PendingIntent and the cleanup are the
     * fiddly parts, and having them written twice is how one of them ends up
     * leaking a receiver.
     */
    private fun finishSession(
        session: PackageInstaller.Session,
        sessionId: Int,
        files: List<File>,
        expectedPackage: String?,
        promise: Promise,
    ) {
        val context = reactApplicationContext
        // Per-session broadcast carries the PackageInstaller result back to us so
        // we can resolve/reject the JS promise.
        val action = "$INSTALL_ACTION.$sessionId"
        val settled = AtomicBoolean(false)
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(c: Context?, intent: Intent?) {
                if (!settled.compareAndSet(false, true)) return
                try { context.unregisterReceiver(this) } catch (_: Exception) {}
                files.forEach { it.delete() }

                val status = intent?.getIntExtra(
                    PackageInstaller.EXTRA_STATUS,
                    PackageInstaller.STATUS_FAILURE,
                ) ?: PackageInstaller.STATUS_FAILURE
                val message = intent?.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: ""

                if (status == PackageInstaller.STATUS_SUCCESS) {
                    val result = Arguments.createMap().apply {
                        putString("status", "success")
                        putString("package", expectedPackage ?: "")
                    }
                    promise.resolve(result)
                } else {
                    promise.reject("INSTALL_FAILED", "Install failed (status $status): $message")
                }
            }
        }
        val filter = IntentFilter(action)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            context.registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            context.registerReceiver(receiver, filter)
        }

        val intent = Intent(action).setPackage(context.packageName)
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            PendingIntent.FLAG_MUTABLE
        } else {
            0
        }
        val pendingIntent = PendingIntent.getBroadcast(context, sessionId, intent, flags)
        session.commit(pendingIntent.intentSender)
        session.close()
    }

    @ReactMethod
    fun addListener(eventName: String) {
        // Required for RN built-in EventEmitter calls
    }

    @ReactMethod
    fun removeListeners(count: Int) {
        // Required for RN built-in EventEmitter calls
    }
}
