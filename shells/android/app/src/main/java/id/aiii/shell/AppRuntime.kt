package id.aiii.shell

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log

internal fun joinsRuntimeStart(wake: Boolean, handoff: Boolean): Boolean = !wake || !handoff

object AppRuntime : mobile.WakeScheduler {
    data class State(val runtime: mobile.Runtime? = null, val error: String? = null, val retry: Boolean = false)
    @Volatile var state = State()
        private set
    val rt: mobile.Runtime? get() = state.runtime
    val url: String? get() = rt?.dashboardURL()
    @Volatile private var appContext: Context? = null
    private val lock = Object()
    private var starting = false
    private val pending = mutableListOf<(String?, String?) -> Unit>()
    private val main = Handler(Looper.getMainLooper())
    private var retryFrom: mobile.Runtime? = null
    private var window: ((State) -> Unit)? = null
    private var foreground = false // main-thread lifecycle truth, reapplied on handoff

    fun observe(callback: (State) -> Unit) {
        window = callback
        callback(state)
    }

    fun detach(callback: (State) -> Unit) { if (window === callback) window = null }

    fun setForeground(live: Boolean) {
        foreground = live
        rt?.setForeground(live)
    }

    fun ensureStarted(context: Context, wake: Boolean = false, done: (String?, String?) -> Unit) {
        appContext = context.applicationContext
        val from: mobile.Runtime?
        val current: State?
        synchronized(lock) {
            if (starting) {
                if (joinsRuntimeStart(wake, retryFrom != null)) { pending.add(done); return }
                current = state
                from = null
            } else {
                current = state.takeIf { it.runtime != null || (it.error != null && retryFrom != null) }
                if (current == null) {
                    pending.add(done)
                    starting = true
                }
                from = retryFrom
            }
        }
        if (current != null) { done(current.runtime?.dashboardURL(), current.error); return }
        main.post { window?.invoke(state) }
        launch(from)
    }

    private fun launch(from: mobile.Runtime?) {
        Thread {
            val result = try {
                val home = appContext!!.filesDir.absolutePath
                val r = if (from == null) mobile.Mobile.start("$home/config.json", home) else from.restart()
                r.setWakeScheduler(this@AppRuntime)
                ForegroundBridge.init(appContext!!)
                r.setForegroundNeedListener(ForegroundBridge)
                State(runtime = r)
            } catch (ex: Exception) {
                State(error = ex.message ?: ex.toString(), retry = from?.canRetryRestart() == true)
            }
            main.post {
                result.runtime?.setForeground(foreground)
                complete(result)
                result.runtime?.let { r ->
                    Thread {
                        if (r.waitForRestart()) main.post { restart(r) }
                    }.start()
                }
            }
        }.start()
    }

    private fun complete(result: State) {
        val cbs: List<(String?, String?) -> Unit>
        synchronized(lock) {
            state = result
            starting = false
            if (result.runtime != null) retryFrom = null
            cbs = pending.toList()
            pending.clear()
        }
        window?.invoke(result)
        for (cb in cbs) cb(result.runtime?.dashboardURL(), result.error)
    }

    private fun restart(expected: mobile.Runtime) {
        synchronized(lock) {
            if (starting || rt !== expected) return
            starting = true
            retryFrom = expected
            state = State()
        }
        window?.invoke(state)
        launch(expected)
    }

    fun retry(context: Context) {
        synchronized(lock) {
            if (!state.retry || starting) return
            state = State()
        }
        ensureStarted(context) { _, _ -> }
    }

    fun wakeWithRuntime(context: Context, done: () -> Unit) {
        ensureStarted(context, wake = true) { _, err ->
            if (err != null) Log.e("AIIOS", "wake cold-start failed: $err")
            rt?.timeWake()
            done()
        }
    }

    fun timeWake() { rt?.timeWake() }


    override fun schedule(atUnixMs: Long) {
        val ctx = appContext ?: return
        val am = ctx.getSystemService(Context.ALARM_SERVICE) as AlarmManager
        val pi = wakeIntent(ctx, PendingIntent.FLAG_UPDATE_CURRENT) ?: return
        val exact = if (Build.VERSION.SDK_INT >= 31) am.canScheduleExactAlarms() else true
        if (exact) {
            am.setExactAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, atUnixMs, pi)
        } else {
            am.setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, atUnixMs, pi)
        }
    }

    override fun cancel() {
        val ctx = appContext ?: return
        val am = ctx.getSystemService(Context.ALARM_SERVICE) as AlarmManager
        val pi = wakeIntent(ctx, PendingIntent.FLAG_NO_CREATE) ?: return
        am.cancel(pi)
        pi.cancel()
    }

    private fun wakeIntent(ctx: Context, flag: Int): PendingIntent? =
        PendingIntent.getBroadcast(
            ctx, 0, Intent(ctx, WakeReceiver::class.java),
            flag or PendingIntent.FLAG_IMMUTABLE
        )
}
