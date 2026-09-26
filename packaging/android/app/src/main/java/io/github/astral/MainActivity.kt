package io.github.astral

import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.Context
import android.graphics.Color
import android.os.Bundle
import android.text.InputType
import android.view.ViewGroup
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
            webViewClient = object : WebViewClient() {
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

        val saved = prefs.getString(KEY_ADDRESS, null)
        if (saved.isNullOrBlank()) askForAddress() else web.loadUrl(saved)
    }

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
