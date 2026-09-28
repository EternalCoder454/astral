package io.github.astral

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * Waits on a reply the PC is writing, so that one finished while you are in
 * another app, or with the screen off, is announced.
 *
 * The page cannot do this itself: once the app is out of sight Android stops
 * running its script, and the reply it was streaming finishes on the PC with
 * nobody told. So when a reply starts the app starts this, quietly, and it
 * asks the PC every few seconds whether the reply is done. It is a
 * foreground service because nothing else keeps running in the background on
 * current Android; its own notification sits in the lowest channel there is.
 */
class ReplyWatcher : Service() {

    @Volatile private var running = false
    private var worker: Thread? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        channels(this)
        val address = intent?.getStringExtra(EXTRA_ADDRESS).orEmpty()
        val token = intent?.getStringExtra(EXTRA_TOKEN).orEmpty()
        val chat = intent?.getStringExtra(EXTRA_CHAT).orEmpty()
        val who = intent?.getStringExtra(EXTRA_WHO).orEmpty().ifBlank { "Astral" }

        val waiting = NotificationCompat.Builder(this, CHANNEL_WAITING)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("$who is replying…")
            .setOngoing(true)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_MIN)
            .setContentIntent(openApp(this))
            .build()
        val type = if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        try {
            ServiceCompat.startForeground(this, ID_WAITING, waiting, type)
        } catch (e: Exception) {
            // Refused, which Android does for a start it considers to be from
            // the background. The reply still lands on the PC; it is only
            // not announced.
            stopSelf()
            return START_NOT_STICKY
        }

        running = false
        worker?.interrupt()
        running = true
        worker = Thread { watch(address, token, chat, who) }.also { it.start() }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        running = false
        worker?.interrupt()
        super.onDestroy()
    }

    /** Asks the PC about the chat until its reply is done, or it gives up. */
    private fun watch(address: String, token: String, chat: String, who: String) {
        val deadline = System.currentTimeMillis() + WATCH_FOR_MS
        while (running && System.currentTimeMillis() < deadline) {
            try {
                Thread.sleep(POLL_MS)
            } catch (e: InterruptedException) {
                return
            }
            val body = fetch("$address/api/chats/$chat", token) ?: continue
            val json = try { JSONObject(body) } catch (e: Exception) { continue }
            if (json.optBoolean("writing", true)) continue
            var text = ""
            val msgs = json.optJSONArray("messages")
            if (msgs != null && msgs.length() > 0) {
                val last = msgs.optJSONObject(msgs.length() - 1)
                if (last != null && last.optString("role") == "assistant") text = last.optString("content")
            }
            // In sight again, the page shows the reply itself.
            if (!MainActivity.visible) announce(this, who, text)
            break
        }
        stopSelf()
    }

    private fun fetch(url: String, token: String): String? = try {
        val conn = (URL(url).openConnection() as HttpURLConnection).apply {
            connectTimeout = 5000
            readTimeout = 10000
            setRequestProperty("Authorization", "Bearer $token")
        }
        try {
            if (conn.responseCode == 200) conn.inputStream.bufferedReader().use { it.readText() } else null
        } finally {
            conn.disconnect()
        }
    } catch (e: Exception) {
        null
    }

    companion object {
        const val EXTRA_ADDRESS = "address"
        const val EXTRA_TOKEN = "token"
        const val EXTRA_CHAT = "chat"
        const val EXTRA_WHO = "who"

        private const val CHANNEL_WAITING = "writing"
        private const val CHANNEL_REPLIES = "replies"
        private const val ID_WAITING = 71
        private const val ID_REPLY = 72
        private const val POLL_MS = 3000L
        private const val WATCH_FOR_MS = 15 * 60 * 1000L

        /** The two channels: one for waiting, silent, one for a finished reply. */
        fun channels(ctx: Context) {
            if (Build.VERSION.SDK_INT < 26) return
            val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_WAITING, "Replies Being Written", NotificationManager.IMPORTANCE_MIN),
            )
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_REPLIES, "Finished Replies", NotificationManager.IMPORTANCE_DEFAULT),
            )
        }

        /** Says a reply has arrived: who, and how it begins. */
        fun announce(ctx: Context, who: String, text: String) {
            if (Build.VERSION.SDK_INT >= 33 &&
                ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) !=
                PackageManager.PERMISSION_GRANTED
            ) return
            channels(ctx)
            val plain = text.replace(Regex("[*_\"“”]"), "").replace(Regex("\\s+"), " ").trim()
            val n = NotificationCompat.Builder(ctx, CHANNEL_REPLIES)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle("$who replied")
                .setContentText(plain.take(140))
                .setStyle(NotificationCompat.BigTextStyle().bigText(plain.take(600)))
                .setAutoCancel(true)
                .setContentIntent(openApp(ctx))
                .build()
            try {
                NotificationManagerCompat.from(ctx).notify(ID_REPLY, n)
            } catch (e: SecurityException) {
                // Notifications were turned off between the check and now.
            }
        }

        /** Takes a reply's notification away once you are looking at the app. */
        fun clear(ctx: Context) = NotificationManagerCompat.from(ctx).cancel(ID_REPLY)

        private fun openApp(ctx: Context): PendingIntent = PendingIntent.getActivity(
            ctx, 0,
            Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }
}
