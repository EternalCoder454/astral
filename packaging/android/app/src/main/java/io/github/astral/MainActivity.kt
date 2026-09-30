package io.github.astral

import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import android.text.InputType
import android.view.HapticFeedbackConstants
import android.view.ViewGroup
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.core.view.WindowInsetsCompat

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

    /**
     * The phone's own voice, for Read Aloud. Started the first time a reply
     * is read rather than at launch, since most sessions never ask for it.
     */
    private var tts: TextToSpeech? = null
    private var ttsReady = false
    private var pendingSpeech: Pair<String, String>? = null

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
            // The page and the app talk through one object with a few methods.
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
        // In a frame rather than on its own: a WebView draws its page over
        // its own padding, so the room for the bars is made around it.
        val frame = FrameLayout(this).apply {
            setBackgroundColor(Color.parseColor("#03050d"))
            addView(web, FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ))
        }
        setContentView(frame)

        // The page is kept clear of the status bar, the navigation bar and
        // the keyboard by padding the view it sits in. Android 15 draws every
        // app that targets it edge to edge, under both bars, and in that mode
        // adjustResize no longer makes room for the keyboard: the top bar sat
        // under the clock and the message box under the keyboard. Doing it
        // here, on every version, means one layout rather than two.
        WindowCompat.setDecorFitsSystemWindows(window, false)
        ViewCompat.setOnApplyWindowInsetsListener(frame) { view, insets ->
            val bars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout(),
            )
            val keyboard = insets.getInsets(WindowInsetsCompat.Type.ime())
            view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, keyboard.bottom))
            WindowInsetsCompat.CONSUMED
        }

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
     * What the page can ask the app to do: update it, which a browser cannot
     * do for itself, and read a reply aloud, which a WebView cannot either.
     */
    private inner class Bridge {
        /** Reads text aloud; the page is called back with id when it ends. */
        @JavascriptInterface
        fun speak(text: String, id: String) {
            runOnUiThread { say(text, id) }
        }

        /**
         * The page's colours changed: paint the status and navigation bars to
         * match, with dark icons when the bars are light.
         */
        @JavascriptInterface
        fun setBars(color: String, dark: Boolean) {
            val c = try { Color.parseColor(color) } catch (e: IllegalArgumentException) { return }
            runOnUiThread {
                window.statusBarColor = c
                window.navigationBarColor = c
                val controls = WindowInsetsControllerCompat(window, window.decorView)
                controls.isAppearanceLightStatusBars = !dark
                controls.isAppearanceLightNavigationBars = !dark
            }
        }

        /** Stops reading. */
        @JavascriptInterface
        fun stopSpeaking() {
            runOnUiThread { tts?.stop() }
        }

        /** A reply has started: keep the screen on, and watch for it. */
        @JavascriptInterface
        fun replyStarted(chat: String, who: String, token: String) {
            runOnUiThread { watchReply(chat, who, token) }
        }

        /** The reply has arrived, or failed; ok says which. */
        @JavascriptInterface
        fun replyEnded(ok: Boolean, who: String, text: String) {
            runOnUiThread { replyDone(ok, who, text) }
        }

        /** A light tap, for sending. */
        @JavascriptInterface
        fun tick() {
            runOnUiThread { web.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP) }
        }

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

    /** Speaks text in the phone's voice, telling the page when it is done. */
    private fun say(text: String, id: String) {
        val engine = tts
        if (engine != null && ttsReady) {
            engine.speak(text, TextToSpeech.QUEUE_FLUSH, null, id)
            return
        }
        pendingSpeech = text to id
        if (engine != null) return // still starting; it speaks when ready
        tts = TextToSpeech(this, TextToSpeech.OnInitListener { status -> started(status, id) })
    }

    /** The voice has started, or failed to; whatever was waiting is spoken. */
    private fun started(status: Int, id: String) {
        ttsReady = status == TextToSpeech.SUCCESS
        if (!ttsReady) {
            // Let go of it, so the next reply read tries again rather than
            // waiting forever on a voice that never started.
            tts?.shutdown()
            tts = null
            pendingSpeech = null
            toPage("astralSpoke", quote(id))
            return
        }
        tts?.setOnUtteranceProgressListener(object : UtteranceProgressListener() {
            override fun onStart(utteranceId: String?) {}
            override fun onDone(utteranceId: String?) {
                toPage("astralSpoke", quote(utteranceId ?: ""))
            }
            @Deprecated("Deprecated in Java")
            override fun onError(utteranceId: String?) {
                toPage("astralSpoke", quote(utteranceId ?: ""))
            }
            override fun onStop(utteranceId: String?, interrupted: Boolean) {
                toPage("astralSpoke", quote(utteranceId ?: ""))
            }
        })
        val waiting = pendingSpeech
        pendingSpeech = null
        if (waiting != null) tts?.speak(waiting.first, TextToSpeech.QUEUE_FLUSH, null, waiting.second)
    }

    override fun onDestroy() {
        tts?.shutdown()
        tts = null
        super.onDestroy()
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

    override fun onStart() {
        super.onStart()
        visible = true
        // Back in sight: a reply's announcement has done its job.
        ReplyWatcher.clear(this)
    }

    override fun onStop() {
        visible = false
        super.onStop()
    }

    /** Starts waiting on a reply, in case the app is left before it lands. */
    private fun watchReply(chat: String, who: String, token: String) {
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        askToNotify()
        val intent = Intent(this, ReplyWatcher::class.java)
            .putExtra(ReplyWatcher.EXTRA_ADDRESS, home().trimEnd('/'))
            .putExtra(ReplyWatcher.EXTRA_TOKEN, token)
            .putExtra(ReplyWatcher.EXTRA_CHAT, chat)
            .putExtra(ReplyWatcher.EXTRA_WHO, who)
        try {
            ContextCompat.startForegroundService(this, intent)
        } catch (e: Exception) {
            // Refused: the reply still lands on the PC, unannounced.
        }
    }

    /** Stops waiting, the reply having arrived on the page. */
    private fun replyDone(ok: Boolean, who: String, text: String) {
        window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        stopService(Intent(this, ReplyWatcher::class.java))
        if (!ok) return
        if (visible) {
            val tick = if (Build.VERSION.SDK_INT >= 30) HapticFeedbackConstants.CONFIRM else HapticFeedbackConstants.VIRTUAL_KEY
            web.performHapticFeedback(tick)
        } else {
            // The page heard it first, while out of sight.
            ReplyWatcher.announce(this, who, text)
        }
    }

    /** Asks once for permission to announce replies, on Android 13 and later. */
    private fun askToNotify() {
        if (Build.VERSION.SDK_INT < 33 || prefs.getBoolean(KEY_ASKED_NOTIFY, false)) return
        if (ContextCompat.checkSelfPermission(this, android.Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
        ) return
        prefs.edit().putBoolean(KEY_ASKED_NOTIFY, true).apply()
        ActivityCompat.requestPermissions(this, arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
    }

    companion object {
        private const val KEY_ADDRESS = "address"
        private const val KEY_ASKED_NOTIFY = "asked_notify"
        private const val DEFAULT_PORT = 8765

        /** Whether the app is on screen, for the watcher deciding to announce. */
        @Volatile
        var visible = false
    }
}
