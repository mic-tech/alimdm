package com.alimdm

import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.WritableMap
import com.facebook.react.bridge.WritableArray
import com.facebook.react.bridge.Arguments
import android.util.Log
import android.content.SharedPreferences
import java.util.Date
import java.text.SimpleDateFormat
import java.util.Locale

class CertificateModule(reactContext: ReactApplicationContext) : ReactContextBaseJavaModule(reactContext) {

  override fun getName(): String {
    return "CertificateModule"
  }

  @ReactMethod
  fun clearAcceptedCertificates(promise: Promise) {
    try {
      val prefs = reactApplicationContext.getSharedPreferences(
        "alimdm_ssl_certs",
        android.content.Context.MODE_PRIVATE
      )
      prefs.edit().clear().apply()
      Log.i("CertificateModule", "All accepted certificates cleared")
      promise.resolve(true)
    } catch (e: Exception) {
      Log.e("CertificateModule", "Error clearing certificates", e)
      promise.reject("ERROR", e.message)
    }
  }

  @ReactMethod
  fun getAcceptedCertificates(promise: Promise) {
    try {
      val prefs = reactApplicationContext.getSharedPreferences(
        "alimdm_ssl_certs",
        android.content.Context.MODE_PRIVATE
      )

      val certificates = Arguments.createArray()
      val allEntries = prefs.all
      val processedFingerprints = mutableSetOf<String>()

      for ((key, value) in allEntries) {
        // Only process "cert_" keys (not "cert_expiry_" or "cert_url_")
        if (key.startsWith("cert_") && !key.contains("_expiry_") && !key.contains("_url_")) {
          val fingerprint = key.substring(5) // Remove "cert_" prefix

          // Avoid duplicates
          if (processedFingerprints.contains(fingerprint)) {
            continue
          }
          processedFingerprints.add(fingerprint)

          val isAccepted = prefs.getBoolean(key, false)
          if (!isAccepted) continue

          val url = prefs.getString("cert_url_$fingerprint", "Unknown") ?: "Unknown"
          val expiryTime = prefs.getLong("cert_expiry_$fingerprint", 0)

          val certInfo = Arguments.createMap()
          certInfo.putString("fingerprint", fingerprint)
          certInfo.putString("url", url)
          certInfo.putDouble("expiryTime", expiryTime.toDouble())

          // Add human-readable expiry date
          val dateFormat = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.getDefault())
          certInfo.putString("expiryDate", dateFormat.format(Date(expiryTime)))

          // Check if expired
          val isExpired = System.currentTimeMillis() > expiryTime
          certInfo.putBoolean("isExpired", isExpired)

          certificates.pushMap(certInfo)
        }
      }

      Log.i("CertificateModule", "Found ${certificates.size()} accepted certificates")
      promise.resolve(certificates)
    } catch (e: Exception) {
      Log.e("CertificateModule", "Error getting certificates", e)
      promise.reject("ERROR", e.message)
    }
  }

  @ReactMethod
  fun removeCertificate(fingerprint: String, promise: Promise) {
    try {
      val prefs = reactApplicationContext.getSharedPreferences(
        "alimdm_ssl_certs",
        android.content.Context.MODE_PRIVATE
      )

      prefs.edit()
        .remove("cert_$fingerprint")
        .remove("cert_expiry_$fingerprint")
        .remove("cert_url_$fingerprint")
        .apply()

      Log.i("CertificateModule", "Certificate removed: $fingerprint")
      promise.resolve(true)
    } catch (e: Exception) {
      Log.e("CertificateModule", "Error removing certificate", e)
      promise.reject("ERROR", e.message)
    }
  }
}
