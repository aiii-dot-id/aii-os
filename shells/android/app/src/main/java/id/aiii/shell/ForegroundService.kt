package id.aiii.shell

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log

object ForegroundBridge : mobile.ForegroundNeedListener {
    private var ctx: Context? = null
    private val main = Handler(Looper.getMainLooper())
    private var stopPending: Runnable? = null

    fun init(context: Context) { ctx = context.applicationContext }

    override fun need(active: Boolean, reason: String) {
        val c = ctx ?: return
        main.post {
            stopPending?.let { main.removeCallbacks(it) }
            stopPending = null
            if (active) {
                val i = Intent(c, ForegroundService::class.java).putExtra("reason", reason)
                try {
                    c.startForegroundService(i)
                } catch (e: Exception) {
                    Log.w("AIIOS", "foreground hold refused by OS: $e")
                }
            } else {
                val r = Runnable {
                    try {
                        c.startService(Intent(c, ForegroundService::class.java).setAction(ForegroundService.ACTION_STOP))
                    } catch (e: Exception) {
                        Log.w("AIIOS", "foreground release refused: $e")
                    }
                }
                stopPending = r
                main.postDelayed(r, 5000)
            }
        }
    }
}

class ForegroundService : Service() {
    companion object { const val ACTION_STOP = "id.aiii.shell.FG_STOP" }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return START_NOT_STICKY
        }
        val channel = "aiios_fg"
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(channel, "AII OS activity", NotificationManager.IMPORTANCE_LOW)
        )
        val reason = intent?.getStringExtra("reason") ?: "working"
        val n: Notification = Notification.Builder(this, channel)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("AII OS is working")
            .setContentText(reason)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(1, n)
        }
        return START_NOT_STICKY
    }
}
