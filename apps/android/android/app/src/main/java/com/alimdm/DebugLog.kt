package com.alimdm

import android.util.Log

object DebugLog {
    private val DEBUG = BuildConfig.DEBUG

    fun d(tag: String, msg: String) {
        if (DEBUG) Log.d(tag, msg)
    }

    fun e(tag: String, msg: String) {
        if (DEBUG) Log.e(tag, msg)
    }

    fun i(tag: String, msg: String) {
        if (DEBUG) Log.i(tag, msg)
    }

    fun w(tag: String, msg: String) {
        if (DEBUG) Log.w(tag, msg)
    }

    // Always log errors in production but without sensitive details
    fun errorProduction(tag: String, msg: String) {
        // Temporairement: toujours afficher le message complet pour debug
        Log.e(tag, msg)
    }
}
