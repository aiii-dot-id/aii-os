package id.aiii.shell

// Process-singleton runtime holder (#2): started at most once; every
// activity instance binds to it; the wake receiver reaches it too.
//
// It is ALSO the OS wake scheduler (MOBILE_PORT §2 — the constructive
// half of the battery work, 2026-08-19): quiesce parks every in-process
// timer while backgrounded, so the Go side hands US its next-due moment
// through mobile.WakeScheduler and we arm ONE AlarmManager alarm at it.
// The OS then invites the runtime back (WakeReceiver → timeWake) at
// exactly the moment something is due — TIME's catch-up does the rest.
import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log

object AppRuntime : mobile.WakeScheduler {
    @Volatile var rt: mobile.Runtime? = null
    @Volatile var url: String? = null
    @Volatile private var appContext: Context? = null
    private val lock = Object()
    private var starting = false
    private val pending = mutableListOf<(String?, String?) -> Unit>()

    // EVERY caller's callback registers and flushes exactly once — the
    // recreation bug (Sol P2-4): a second activity arriving mid-start
    // was returned UNREGISTERED, and if the first activity died before
    // start finished, nobody was left to load the URL: a blank window
    // over a healthy runtime. The pending list is the whole fix.
    //
    // The home directory is derived HERE (filesDir), not passed: the
    // wake receiver and the activity must resolve the identical home,
    // and two call sites deriving it is how they drift.
    fun ensureStarted(context: Context, done: (String?, String?) -> Unit) {
        appContext = context.applicationContext
        val runNow: Boolean
        synchronized(lock) {
            if (rt != null) {
                runNow = true
            } else {
                pending.add(done)
                runNow = false
                if (starting) return
                starting = true
            }
        }
        if (runNow) { done(url, null); return }
        Thread {
            var u: String? = null
            var e: String? = null
            try {
                val home = appContext!!.filesDir.absolutePath
                val r = mobile.Mobile.start("$home/config.json", home)
                // The OUTBOUND wake seam, wired right after Start (its
                // inbound sibling is timeWake below): from here on TIME
                // drives schedule()/cancel() with its next-due moment.
                r.setWakeScheduler(this@AppRuntime)
                // The foreground-need seam (internal/foreground): the
                // Go registry says WHETHER the process must stay alive
                // and why; the OS half — startForeground, the visible
                // notification — lives in ForegroundBridge.
                ForegroundBridge.init(appContext!!)
                r.setForegroundNeedListener(ForegroundBridge)
                rt = r
                u = r.dashboardURL()
                url = u
            } catch (ex: Exception) {
                e = ex.message ?: ex.toString()
            }
            val cbs: List<(String?, String?) -> Unit>
            synchronized(lock) {
                starting = false
                cbs = pending.toList()
                pending.clear()
            }
            for (cb in cbs) cb(u, e)
        }.start()
    }

    // The wake path when the broadcast RELAUNCHED the process. The old
    // comment said "a wake before Start is meaningless" — exactly
    // backwards for a process the wake itself created (Sol P1-2): the
    // runtime never started, TIME never re-armed the next alarm, and
    // one process death silenced wakes forever. The wake IS the reason
    // to start.
    fun wakeWithRuntime(context: Context, done: () -> Unit) {
        ensureStarted(context) { _, err ->
            if (err != null) Log.e("AIIOS", "wake cold-start failed: $err")
            rt?.timeWake()
            done()
        }
    }

    fun timeWake() { rt?.timeWake() }

    // --- mobile.WakeScheduler ---
    // Called from Go's scheduler goroutine, never the main thread;
    // AlarmManager is binder-backed and thread-safe, so no hopping.

    // ONE next-due wake, absolute wall milliseconds (RTC_WAKEUP). Exact
    // when the operator granted exact alarms (SCHEDULE_EXACT_ALARM is
    // special app access on Android 12+, user-grantable in Settings);
    // inexact-while-idle otherwise — Doze batches those into windows
    // (order of ~15 min), which TIME tolerates by design: a late wake
    // evaluates late, never lost. Reusing the SAME PendingIntent means
    // every set() replaces the previous alarm — the one-slot contract
    // comes free from the platform.
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

    // Nothing worth waking for: drop the alarm and its PendingIntent.
    // FLAG_NO_CREATE keeps a cancel from minting the thing it means to
    // kill — if no alarm was ever set, there is nothing to do.
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
