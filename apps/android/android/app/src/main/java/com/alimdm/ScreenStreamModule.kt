package com.alimdm

import android.graphics.Bitmap
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import android.view.KeyEvent
import android.view.PixelCopy
import com.facebook.react.bridge.*
import com.facebook.react.common.LifecycleState
import com.facebook.react.modules.core.DeviceEventManagerModule
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.OutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Live view: captures the screen and posts JPEG frames to the console.
 *
 * Runs entirely in Kotlin. Pushing frames across the React bridge as base64 at
 * several frames a second would cost more than the capture itself and would
 * stutter the kiosk it is trying to show.
 *
 * Two capture paths, because Android gives no single one that is both fast and
 * complete:
 *
 *  - [PixelCopy] on our own window. Fast, unlimited, and unaffected by the
 *    Device Owner screen-capture policy — but it only sees this app, so it goes
 *    blank behind an external app.
 *  - [AliMdmAccessibilityService.captureScreen], which sees everything but is
 *    rate-limited by the platform to one frame per second, and is blocked
 *    outright while the Device Owner screen-capture policy is on.
 *
 * So: PixelCopy while the kiosk is in front, the accessibility path at 1fps
 * when an external app is. The policy is lifted for the length of a session and
 * restored afterwards — which does mean a pupil could take a screenshot during
 * a live view, and is the reason a session is not left running.
 */
class ScreenStreamModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName() = "ScreenStreamModule"

    companion object {
        private const val TAG = "ScreenStream"

        /** The platform will not take accessibility screenshots faster than this. */
        private const val ACCESSIBILITY_MIN_INTERVAL_MS = 1100L

        /**
         * How long a scroll drag takes. Long enough that Android reads it as a
         * scroll rather than a fling, short enough not to feel stuck.
         */
        private const val SCROLL_DURATION_MS = 260L

        /** Give up on a session that cannot reach the server for this long. */
        private const val MAX_CONSECUTIVE_FAILURES = 10

        const val EVENT_STATE = "AliMdmScreenStreamState"
    }

    private val running = AtomicBoolean(false)
    private var worker: Thread? = null

    // A single reused thread for PixelCopy callbacks. One HandlerThread per
    // frame would be a thread per 100ms.
    private var copyThread: HandlerThread? = null
    private var copyHandler: Handler? = null

    /** Whether we turned the screen-capture policy off, and so must put it back. */
    private var liftedCapturePolicy = false

    /**
     * The part of the display the last frame showed, in screen coordinates.
     *
     * A frame is either our own window (PixelCopy) or the whole display
     * (accessibility), and on these tablets the window is portrait inside a
     * landscape display. A tap arrives as a fraction of the picture the
     * operator clicked, so it can only be placed correctly against the
     * rectangle that picture covered.
     */
    @Volatile
    private var lastFrameRect: android.graphics.Rect? = null

    @ReactMethod
    fun isStreaming(promise: Promise) = promise.resolve(running.get())

    /**
     * Begin a session. Returns as soon as the loop is running; frames start
     * arriving at the console shortly after.
     */
    @ReactMethod
    fun start(options: ReadableMap, promise: Promise) {
        if (running.get()) {
            promise.resolve(false) // already streaming; not an error
            return
        }
        val cloudUrl = options.getStringOr("cloudUrl")
        val deviceId = options.getStringOr("deviceId")
        val apiKey = options.getStringOr("apiKey")
        if (cloudUrl.isEmpty() || deviceId.isEmpty() || apiKey.isEmpty()) {
            promise.reject("BAD_ARGS", "cloudUrl, deviceId and apiKey are required")
            return
        }
        val fps = if (options.hasKey("fps")) options.getInt("fps").coerceIn(1, 15) else 6
        val maxWidth = if (options.hasKey("maxWidth")) options.getInt("maxWidth").coerceIn(320, 1920) else 900
        val quality = if (options.hasKey("quality")) options.getInt("quality").coerceIn(20, 95) else 55

        // Lift the Device Owner capture block for the session, so the
        // accessibility path can see an external app. PixelCopy does not need
        // this, but we cannot know in advance what will be on screen.
        liftedCapturePolicy = try {
            if (KioskModule.isScreenCapturePolicyBlocked(reactContext)) {
                KioskModule.setScreenCapturePolicyBlocked(reactContext, false)
            } else {
                false
            }
        } catch (e: Exception) {
            Log.w(TAG, "Could not lift the screen-capture policy: ${e.message}")
            false
        }

        copyThread = HandlerThread("ScreenStreamCopy").apply { start() }
        copyHandler = Handler(copyThread!!.looper)

        running.set(true)
        emitState(true, "")
        worker = Thread({ loop(cloudUrl.trimEnd('/'), deviceId, apiKey, fps, maxWidth, quality) },
            "ScreenStreamLoop").apply {
            priority = Thread.NORM_PRIORITY - 1 // never starve the kiosk UI
            start()
        }
        promise.resolve(true)
    }

    @ReactMethod
    fun stop(promise: Promise) {
        stopInternal("stopped by request")
        promise.resolve(true)
    }

    private fun stopInternal(reason: String) {
        if (!running.getAndSet(false)) return
        Log.d(TAG, "Live view ending: $reason")
        try {
            copyThread?.quitSafely()
        } catch (_: Exception) {
        }
        copyThread = null
        copyHandler = null
        // Put the policy back exactly as we found it. Leaving it off would let
        // anyone screenshot the tablet indefinitely.
        if (liftedCapturePolicy) {
            try {
                KioskModule.setScreenCapturePolicyBlocked(reactContext, true)
            } catch (e: Exception) {
                Log.e(TAG, "Could not restore the screen-capture policy: ${e.message}")
            }
            liftedCapturePolicy = false
        }
        emitState(false, reason)
    }

    private fun loop(
        cloudUrl: String, deviceId: String, apiKey: String,
        fps: Int, maxWidth: Int, quality: Int,
    ) {
        val url = URL("$cloudUrl/api/v1/devices/$deviceId/stream/frame")
        val targetIntervalMs = (1000L / fps).coerceAtLeast(50L)
        var failures = 0
        var lastAccessibilityAt = 0L
        var triedEnablingAccessibility = false

        while (running.get()) {
            val startedAt = System.currentTimeMillis()
            try {
                val foreground = reactContext.currentActivity != null &&
                    reactContext.lifecycleState == LifecycleState.RESUMED

                var bitmap: Bitmap? = null
                if (foreground) {
                    bitmap = capturePixelCopy()
                }
                if (bitmap == null) {
                    // Either an external app is in front, or PixelCopy failed.
                    // This path is rate-limited by the platform, so do not even
                    // ask more than once a second.
                    val since = startedAt - lastAccessibilityAt
                    if (since < ACCESSIBILITY_MIN_INTERVAL_MS) {
                        Thread.sleep(ACCESSIBILITY_MIN_INTERVAL_MS - since)
                    }
                    if (!running.get()) break
                    // The service is what sees an external app. Try once per
                    // session to turn it on if it is off; retrying every frame
                    // would spend the whole session waiting for a bind that is
                    // not coming.
                    if (!AliMdmAccessibilityService.isRunning() && !triedEnablingAccessibility) {
                        triedEnablingAccessibility = true
                        val reason = AliMdmAccessibilityService.ensureRunningOrReason(reactContext)
                        if (reason != null) {
                            Log.w(TAG, "Live view will be blank behind an external app: " +
                                "the accessibility service is off and $reason")
                        }
                    }
                    lastAccessibilityAt = System.currentTimeMillis()
                    bitmap = AliMdmAccessibilityService.captureScreen(3000)
                    // This path returns the whole display, not our window.
                    if (bitmap != null) lastFrameRect = fullDisplayRect()
                }
                if (bitmap == null) {
                    failures++
                    if (failures >= MAX_CONSECUTIVE_FAILURES) {
                        stopInternal("could not capture the screen")
                        return
                    }
                    Thread.sleep(500)
                    continue
                }

                val jpeg = encode(bitmap, maxWidth, quality)
                bitmap.recycle()

                when (post(url, apiKey, jpeg)) {
                    PostResult.CONTINUE -> failures = 0
                    PostResult.STOP -> {
                        stopInternal("nobody is watching")
                        return
                    }
                    PostResult.FAILED -> {
                        failures++
                        if (failures >= MAX_CONSECUTIVE_FAILURES) {
                            stopInternal("lost contact with the console")
                            return
                        }
                        Thread.sleep(500)
                    }
                }
            } catch (e: InterruptedException) {
                Thread.currentThread().interrupt()
                break
            } catch (e: Exception) {
                Log.w(TAG, "Frame failed: ${e.message}")
                failures++
                if (failures >= MAX_CONSECUTIVE_FAILURES) {
                    stopInternal("too many errors: ${e.message}")
                    return
                }
            }

            val elapsed = System.currentTimeMillis() - startedAt
            if (elapsed < targetIntervalMs) {
                try {
                    Thread.sleep(targetIntervalMs - elapsed)
                } catch (e: InterruptedException) {
                    Thread.currentThread().interrupt()
                    break
                }
            }
        }
        stopInternal("loop ended")
    }

    /**
     * Carry out what an operator did: taps, Back/Home, and typed text.
     *
     * Everything goes through the accessibility service, which is the only way
     * to reach an app that is not ours — and the same dependency screen capture
     * of another app already has. Each event puts the "Remotely controlled"
     * banner on screen: a tablet that starts operating itself should say why.
     */
    private fun applyInput(events: org.json.JSONArray) {
        for (i in 0 until events.length()) {
            val e = events.optJSONObject(i) ?: continue
            val handled = when (e.optString("type")) {
                "tap" -> {
                    val rect = lastFrameRect ?: fullDisplayRect()
                    val fx = e.optDouble("x", -1.0)
                    val fy = e.optDouble("y", -1.0)
                    if (fx < 0 || fx > 1 || fy < 0 || fy > 1) {
                        false
                    } else {
                        AliMdmAccessibilityService.tapAt(
                            (rect.left + fx * rect.width()).toFloat(),
                            (rect.top + fy * rect.height()).toFloat(),
                        )
                    }
                }
                "key" -> {
                    val code = when (e.optString("key")) {
                        "back" -> KeyEvent.KEYCODE_BACK
                        "home" -> KeyEvent.KEYCODE_HOME
                        "enter" -> KeyEvent.KEYCODE_ENTER
                        "up" -> KeyEvent.KEYCODE_DPAD_UP
                        "down" -> KeyEvent.KEYCODE_DPAD_DOWN
                        "left" -> KeyEvent.KEYCODE_DPAD_LEFT
                        "right" -> KeyEvent.KEYCODE_DPAD_RIGHT
                        else -> 0
                    }
                    code != 0 && AliMdmAccessibilityService.sendKey(code)
                }
                "scroll" -> {
                    // Down means "show me what is further down the page", which
                    // is a finger moving up. Named for what the operator wants
                    // rather than which way the finger goes, because the button
                    // says Scroll down and that is what it must do.
                    val rect = lastFrameRect ?: fullDisplayRect()
                    val x = (rect.left + rect.width() / 2).toFloat()
                    val near = (rect.top + rect.height() * 0.70).toFloat()
                    val far = (rect.top + rect.height() * 0.30).toFloat()
                    val down = e.optString("dir") == "down"
                    AliMdmAccessibilityService.swipeBetween(
                        x, if (down) near else far,
                        x, if (down) far else near,
                        SCROLL_DURATION_MS,
                    )
                }
                "text" -> AliMdmAccessibilityService.sendText(e.optString("text"))
                else -> false
            }
            if (!handled) {
                Log.w(TAG, "Could not carry out ${e.optString("type")}; is the accessibility service on?")
            }
            RemoteControlBanner.show(reactContext)
        }
    }

    private fun fullDisplayRect(): android.graphics.Rect {
        val m = reactContext.resources.displayMetrics
        return android.graphics.Rect(0, 0, m.widthPixels, m.heightPixels)
    }

    /** PixelCopy of our own window, or null when it is not on screen. */
    private fun capturePixelCopy(): Bitmap? {
        val handler = copyHandler ?: return null
        var result: Bitmap? = null
        val latch = CountDownLatch(1)

        UiThreadUtil.runOnUiThread {
            try {
                val window = reactContext.currentActivity?.window
                val decor = window?.decorView
                if (window == null || decor == null || decor.width <= 0 || decor.height <= 0) {
                    latch.countDown()
                    return@runOnUiThread
                }
                // Where this window sits on the display, so a tap taken from
                // the frame lands where it was aimed.
                val at = IntArray(2).also { decor.getLocationOnScreen(it) }
                lastFrameRect = android.graphics.Rect(
                    at[0], at[1], at[0] + decor.width, at[1] + decor.height)
                val bmp = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
                PixelCopy.request(window, bmp, { code ->
                    if (code == PixelCopy.SUCCESS) {
                        result = bmp
                    } else {
                        bmp.recycle()
                    }
                    latch.countDown()
                }, handler)
            } catch (e: Exception) {
                // "Window doesn't have a backing surface" while an external app
                // is in front; the caller falls back to accessibility capture.
                latch.countDown()
            }
        }
        return if (latch.await(2, TimeUnit.SECONDS)) result else null
    }

    /** Scale to a sane width and JPEG-encode. Full-resolution PNGs would not fit the frame budget. */
    private fun encode(src: Bitmap, maxWidth: Int, quality: Int): ByteArray {
        val scaled = if (src.width > maxWidth) {
            val h = (src.height.toLong() * maxWidth / src.width).toInt().coerceAtLeast(1)
            Bitmap.createScaledBitmap(src, maxWidth, h, true)
        } else {
            src
        }
        val out = ByteArrayOutputStream(64 * 1024)
        scaled.compress(Bitmap.CompressFormat.JPEG, quality, out)
        if (scaled !== src) scaled.recycle()
        return out.toByteArray()
    }

    private enum class PostResult { CONTINUE, STOP, FAILED }

    private fun post(url: URL, apiKey: String, jpeg: ByteArray): PostResult {
        var conn: HttpURLConnection? = null
        return try {
            conn = (url.openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"
                doOutput = true
                connectTimeout = 8000
                readTimeout = 8000
                setRequestProperty("Authorization", "Bearer $apiKey")
                setRequestProperty("Content-Type", "image/jpeg")
                setFixedLengthStreamingMode(jpeg.size)
            }
            val os: OutputStream = conn.outputStream
            os.write(jpeg)
            os.flush()
            os.close()

            val code = conn.responseCode
            if (code !in 200..299) return PostResult.FAILED
            val body = conn.inputStream.bufferedReader().use { it.readText() }
            // The reply to a frame is the channel back to the tablet: whether
            // anyone is still watching, and anything an operator has done since
            // the last frame. It is already made several times a second while
            // someone is watching, and never when nobody is.
            val reply = try {
                JSONObject(body)
            } catch (e: Exception) {
                Log.w(TAG, "Frame reply was not JSON: ${e.message}")
                null
            }
            reply?.optJSONArray("input")?.let { applyInput(it) }
            if (reply?.optBoolean("continue", true) == false) PostResult.STOP else PostResult.CONTINUE
        } catch (e: Exception) {
            Log.w(TAG, "Frame post failed: ${e.message}")
            PostResult.FAILED
        } finally {
            conn?.disconnect()
        }
    }

    private fun emitState(streaming: Boolean, reason: String) {
        try {
            reactContext
                .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
                .emit(EVENT_STATE, Arguments.createMap().apply {
                    putBoolean("streaming", streaming)
                    putString("reason", reason)
                })
        } catch (e: Exception) {
            // The bridge can be gone during teardown; the loop's own state is
            // what matters, so this is not worth failing over.
        }
    }

    override fun onCatalystInstanceDestroy() {
        super.onCatalystInstanceDestroy()
        stopInternal("app shutting down")
    }
}

private fun ReadableMap.getStringOr(key: String): String =
    if (hasKey(key)) (getString(key) ?: "") else ""
