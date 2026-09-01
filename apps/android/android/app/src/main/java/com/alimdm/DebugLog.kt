package com.alimdm

import android.util.Log
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

object DebugLog {
    private val DEBUG = BuildConfig.DEBUG

    /**
     * The last few hundred lines, kept in memory so an operator can ask a tablet
     * what it has been doing.
     *
     * These messages only ever went to logcat, and in a release build d/i/w did
     * not even go there — so on a tablet in a classroom, the app's account of
     * its own behaviour did not exist. Diagnosing anything meant a USB cable and
     * being in the room. Recording here is unconditional; whether a line also
     * reaches logcat is still a debug-build question.
     *
     * In memory only, and capped: this is a diagnostic aid, not an audit trail,
     * and a tablet's log has no business surviving on disk.
     */
    private const val MAX_LINES = 600
    private val buffer = ArrayDeque<String>(MAX_LINES)
    private val stamp = SimpleDateFormat("MM-dd HH:mm:ss.SSS", Locale.US)

    private fun record(level: String, tag: String, msg: String) {
        val line = "${stamp.format(Date())} $level/$tag: $msg"
        synchronized(buffer) {
            if (buffer.size >= MAX_LINES) buffer.removeFirst()
            buffer.addLast(line)
        }
    }

    /** Newest last, as a log reads. */
    fun recent(): String = synchronized(buffer) { buffer.joinToString("\n") }

    fun d(tag: String, msg: String) {
        record("D", tag, msg)
        if (DEBUG) Log.d(tag, msg)
    }

    fun e(tag: String, msg: String) {
        record("E", tag, msg)
        if (DEBUG) Log.e(tag, msg)
    }

    fun i(tag: String, msg: String) {
        record("I", tag, msg)
        if (DEBUG) Log.i(tag, msg)
    }

    fun w(tag: String, msg: String) {
        record("W", tag, msg)
        if (DEBUG) Log.w(tag, msg)
    }

    // Always log errors in production but without sensitive details
    fun errorProduction(tag: String, msg: String) {
        record("E", tag, msg)
        Log.e(tag, msg)
    }
}
