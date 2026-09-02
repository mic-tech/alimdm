package com.alimdm

import android.content.Context
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import android.util.TypedValue
import android.view.Gravity
import android.view.WindowManager
import android.widget.TextView

/**
 * "Remotely controlled" along the bottom of the screen, for as long as someone
 * is driving the tablet from the console.
 *
 * Anyone holding a device that starts operating itself should be able to see
 * why. It is shown for control only, not for watching: these are shared
 * classroom tablets rather than anyone's own, and a banner during every routine
 * screenshot would be noise that teaches people to ignore it.
 *
 * An overlay window rather than anything in our own view tree, because control
 * is most useful while a pupil is inside another app — which is precisely when
 * our own window is not on screen. Needs the draw-over-other-apps permission;
 * without it there is nothing to show and the control still works, so this
 * degrades quietly rather than blocking it.
 */
object RemoteControlBanner {

    private const val TAG = "RemoteControlBanner"

    /** Hidden this long after the last event, so a pause does not flicker it. */
    private const val LINGER_MS = 5_000L

    private val main = Handler(Looper.getMainLooper())
    private var view: TextView? = null
    private val hide = Runnable { remove() }

    /** Call on every input event: shows the banner, and keeps it up. */
    fun show(context: Context) {
        main.post {
            try {
                if (view == null) add(context)
                main.removeCallbacks(hide)
                main.postDelayed(hide, LINGER_MS)
            } catch (e: Exception) {
                Log.w(TAG, "Could not show the banner: ${e.message}")
            }
        }
    }

    private fun add(context: Context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M && !Settings.canDrawOverlays(context)) {
            Log.w(TAG, "No draw-over-other-apps permission: the tablet will be controlled with nothing on screen to say so")
            return
        }
        val app = context.applicationContext
        val wm = app.getSystemService(Context.WINDOW_SERVICE) as WindowManager
        val text = TextView(app).apply {
            text = app.getString(R.string.remote_control_banner)
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 12f)
            val padH = (16 * app.resources.displayMetrics.density).toInt()
            val padV = (6 * app.resources.displayMetrics.density).toInt()
            setPadding(padH, padV, padH, padV)
            background = GradientDrawable().apply {
                setColor(Color.argb(150, 0, 0, 0))
                cornerRadius = 999f
            }
        }
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O)
                WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY
            else
                @Suppress("DEPRECATION")
                WindowManager.LayoutParams.TYPE_SYSTEM_ALERT,
            // Not focusable and not touchable: it must never take a tap meant
            // for the app underneath, including the taps being sent remotely.
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
                WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                WindowManager.LayoutParams.FLAG_HARDWARE_ACCELERATED,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.BOTTOM or Gravity.CENTER_HORIZONTAL
            y = (24 * app.resources.displayMetrics.density).toInt()
        }
        wm.addView(text, params)
        view = text
        Log.i(TAG, "Remote control banner shown")
    }

    private fun remove() {
        val v = view ?: return
        view = null
        try {
            val wm = v.context.applicationContext
                .getSystemService(Context.WINDOW_SERVICE) as WindowManager
            wm.removeView(v)
            Log.i(TAG, "Remote control banner hidden")
        } catch (e: Exception) {
            Log.w(TAG, "Could not hide the banner: ${e.message}")
        }
    }
}
