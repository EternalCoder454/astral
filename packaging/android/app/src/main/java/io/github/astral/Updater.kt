package io.github.astral

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/**
 * Installing a newer Astral.
 *
 * Android will not update a sideloaded app by itself, so this asks: it fetches
 * the build from the PC the app is already paired with and hands it to the
 * system installer, which is the only thing that can actually replace an app.
 *
 * Nothing here is trusted with much. The installer shows what it is about to
 * do, and it refuses a build signed with a different key from the one already
 * on the phone, so a file altered on the way across the network is rejected by
 * Android rather than by this code.
 */
object Updater {

    /** The directory downloads land in, which is the only one the provider shares. */
    private fun dir(activity: Activity) = File(activity.cacheDir, "updates").apply { mkdirs() }

    /**
     * True when this app is allowed to ask the installer. Below Android 8 the
     * permission is granted at install time and there is nothing to check.
     */
    fun canInstall(activity: Activity): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.O || activity.packageManager.canRequestPackageInstalls()

    /**
     * Sends the user to the one screen that can grant it. Android does not
     * allow an app to ask for this in a dialog: it is a settings page, per app.
     */
    fun askForPermission(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        activity.startActivity(
            Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${activity.packageName}")),
        )
    }

    /**
     * Downloads [version] from the paired PC and opens the installer.
     *
     * [base] is the address the app is already talking to, so the download
     * comes from the same machine as everything else and works on a network
     * with no way out to the internet.
     *
     * [onProgress] is called with whole percentages, or -1 when the size is
     * not known, so the page can say something while a few megabytes arrive.
     */
    fun download(
        activity: Activity,
        base: String,
        token: String,
        version: String,
        onProgress: (Int) -> Unit,
        onError: (String) -> Unit,
    ) {
        Thread {
            try {
                val url = URL("${base.trimEnd('/')}/api/app/download?version=$version")
                val conn = (url.openConnection() as HttpURLConnection).apply {
                    setRequestProperty("Authorization", "Bearer $token")
                    connectTimeout = 15_000
                    readTimeout = 60_000
                }
                if (conn.responseCode != 200) {
                    onError("Your PC answered ${conn.responseCode}")
                    return@Thread
                }
                val total = conn.contentLength
                // A part file, renamed only once it is whole: an interrupted
                // download must never be handed to the installer as if it were
                // a build.
                val part = File(dir(activity), "astral-$version.apk.part")
                val done = File(dir(activity), "astral-$version.apk")
                conn.inputStream.use { input ->
                    part.outputStream().use { out ->
                        val buf = ByteArray(64 * 1024)
                        var read = 0L
                        var last = -1
                        while (true) {
                            val n = input.read(buf)
                            if (n < 0) break
                            out.write(buf, 0, n)
                            read += n
                            val pct = if (total > 0) (read * 100 / total).toInt() else -1
                            if (pct != last) {
                                last = pct
                                onProgress(pct)
                            }
                        }
                    }
                }
                if (!part.renameTo(done)) {
                    onError("Could not finish the download")
                    return@Thread
                }
                activity.runOnUiThread { open(activity, done, onError) }
            } catch (e: Exception) {
                onError(e.message ?: "The download failed")
            }
        }.start()
    }

    /** Hands a downloaded build to the system installer. */
    private fun open(activity: Activity, apk: File, onError: (String) -> Unit) {
        try {
            val uri = FileProvider.getUriForFile(activity, "${activity.packageName}.files", apk)
            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            activity.startActivity(intent)
        } catch (e: Exception) {
            onError(e.message ?: "Android would not open the installer")
        }
    }

    /** Removes anything left behind by an earlier update. */
    fun tidy(activity: Activity) {
        dir(activity).listFiles()?.forEach { it.delete() }
    }
}
