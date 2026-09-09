package com.alimdm

// What the package verifier looks like from inside the app, and why nothing
// here tries to change it any more.
//
// Play Protect refuses to install an app it does not recognise, which for a
// fleet's own in-house builds is every one of them on every newly enrolled
// tablet. Someone then has to walk to the device and approve it, which defeats
// the point of provisioning by scanning a code — so it was worth one experiment
// to find out whether the app could switch the verifier off itself.
//
// It cannot, and the measurement was unambiguous. On a Device Owner tablet
// running Android 15, build 1.2.61:
//
//   setGlobalSetting("package_verifier_enable", "0")  -> SecurityException
//   Settings.Global.putInt(same)                      -> no WRITE_SECURE_SETTINGS
//   reading package_verifier_enable                   -> -1, i.e. absent
//   reading verifier_verify_adb_installs              -> -1, absent
//   reading package_verifier_user_consent             -> -1, absent
//
// The last three matter more than the first two. Those settings are not merely
// unwritable, they do not exist on the device: the platform verifier knobs are
// gone from modern Android and Play Protect keeps its own state inside Google
// Play services. There is nothing here to set, whatever permission you hold.
// That is why the toggle lives in the Play Store app rather than in Settings,
// and why turning it off is a step in the enrolment runbook rather than
// something the agent can do.
//
// Left as a record so the experiment is not repeated. DiagnosticsModule still
// reports the three values, which cost nothing to read and identify this class
// of failure immediately.
object VerifierControl {
    /** Retained so diagnostics keep a stable shape across the fleet. */
    const val lastAttempt: String = "not attempted \u2014 no settable verifier on modern Android"
}
