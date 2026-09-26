package io.github.astral

import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.text.InputType
import android.view.ViewGroup
import android.webkit.JavascriptInterface
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.EditText
import android.widget.LinearLayout
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity

/**
 * Astral on a phone.
 *
 * This app is a window onto a PC, and nothing more. The library and the model
 * both live on that machine, because a 27B model does not run on a phone; what
 * this adds over opening the page in a browser is an icon, a full screen, and
 * remembering the address so it does not have to be typed again.
 *
 * Everything you see is served by your own machine. There is no account, no
 * server belonging to anyone else, and nothing leaves your network.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var web: WebView

    /** Where the PC is. Kept here because it is the one thing this app knows. */
    private val prefs by lazy { getSharedPreferences("astral", Context.MODE_PRIVATE) }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(saved: Bundle?) {
        super.onCreate(saved)

        web = WebView(this).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            setBackgroundColor(Color.parseColor("#03050d"))
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true // the paired token lives here
            settings.cacheMode = WebSettings.LOAD_DEFAULT
            // The page is served by the user's own machine and sized for this
            // screen already, so nothing is zoomed or reflowed on its behalf.
            settings.useWideViewPort = false
            settings.loadWithOverviewMode = false
            settings.setSupportZoom(false)
            // The page and the app talk through one object with three methods.
            //
            // addJavascriptInterface hands the page real code, which is only
            // safe because of the rule below it: this WebView loads the one
            // address it was paired with and refuses to navigate anywhere
            // else, so the only page that can reach this is the one served by
            // the machine the user already trusts with their library.
            addJavascriptInterface(Bridge(), "AstralApp")
            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(
                    view: WebView,
                    request: WebResourceRequest,
                ): Boolean {
                    val url = request.url.toString()
                    if (url.startsWith(home())) return false
                    // A link out goes to the browser rather than inside the
                    // app, so nothing else is ever loaded where the bridge is.
                    return try {
                        startActivity(Intent(Intent.ACTION_VIEW, request.url))
                        true
                    } catch (_: Exception) {
                        true
                    }
                }

                override fun onReceivedError(
                    view: WebView,
                    request: WebResourceRequest,
                    error: WebResourceError,
                ) {
                    if (request.isForMainFrame) askForAddress(couldNotReach = true)
                }
            }
        }
        setContentView(web)

        // The hardware back button walks back through the app rather than
        // closing it, which is what every other Android app does.
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (web.canGoBack()) web.goBack() else finish()
            }
        })

        Updater.tidy(this)

        val saved = prefs.getString(KEY_ADDRESS, null)
        if (saved.isNullOrBlank()) askForAddress() else web.loadUrl(saved)
    }

    /** The address this app is paired with, or empty before it is set. */
    private fun home(): String = prefs.getString(KEY_ADDRESS, "") ?: ""

    /**
     * What the page can ask the app to do. Three things, all about updating,
     * because that is the only thing a browser cannot do for itself.
     */
    private inner class Bridge {
        /** Tells the page it is running inside the app rather than a browser. */
        @JavascriptInterface
        fun version(): String =
            packageManager.getPackageInfo(packageName, 0).versionName ?: ""

        /** Whether Android will currently let this app start an install. */
        @JavascriptInterface
        fun canInstall(): Boolean = Updater.canInstall(this@MainActivity)

        /** Opens the settings page where that permission is granted. */
        @JavascriptInterface
        fun askToInstall() {
            runOnUiThread { Updater.askForPermission(this@MainActivity) }
        }

        /**
         * Downloads a version from the paired PC and opens the installer.
         * Progress and failures go back to the page, which is where the user
         * is looking.
         */
        @JavascriptInterface
        fun install(version: String, token: String) {
            Updater.download(
                this@MainActivity, home(), token, version,
                onProgress = { pct -> toPage("astralUpdateProgress", pct.toString()) },
                onError = { msg -> toPage("astralUpdateFailed", quote(msg)) },
            )
        }
    }

    /** Calls a function on the page, if it has one. */
    private fun toPage(fn: String, arg: String) {
        runOnUiThread {
            web.evaluateJavascript("window.$fn && window.$fn($arg)", null)
        }
    }

    private fun quote(s: String): String =
        "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", " ") + "\""

    override fun onSaveInstanceState(out: Bundle) {
        super.onSaveInstanceState(out)
        web.saveState(out)
    }

    /**
     * Asks where the PC is. The address is shown by Astral on that machine,
     * under Settings, Phone access.
     */
    private fun askForAddress(couldNotReach: Boolean = false) {
        val field = EditText(this).apply {
            inputType = InputType.TYPE_TEXT_VARIATION_URI
            hint = "192.168.1.20:$DEFAULT_PORT"
            setText(prefs.getString(KEY_ADDRESS, "") ?: "")
        }
        val pad = (resources.displayMetrics.density * 20).toInt()
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, 0)
            addView(field)
        }
        AlertDialog.Builder(this)
            .setTitle(if (couldNotReach) "Could not reach your PC" else "Where is your PC?")
            .setMessage(
                if (couldNotReach) {
                    "Check that Astral is open on it, that phone access is switched on, " +
                        "and that this phone is on the same Wi-Fi."
                } else {
                    "Open Astral on your PC, go to Settings, then Phone access, and type " +
                        "the address it shows."
                },
            )
            .setView(box)
            .setCancelable(false)
            .setPositiveButton("Connect") { _, _ ->
                val url = normalise(field.text.toString())
                prefs.edit().putString(KEY_ADDRESS, url).apply()
                web.loadUrl(url)
            }
            .show()
    }

    /**
     * Turns what someone types into an address. A bare "192.168.1.20" is what
     * people read off a screen and it is not a URL, so it is made into one
     * rather than refused.
     */
    private fun normalise(input: String): String {
        var s = input.trim().removeSuffix("/")
        if (s.isEmpty()) return s
        if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://$s"
        val afterScheme = s.substringAfter("://")
        if (!afterScheme.contains(":")) s = "$s:$DEFAULT_PORT"
        return s
    }

    companion object {
        private const val KEY_ADDRESS = "address"
        private const val DEFAULT_PORT = 8765
    }
}
