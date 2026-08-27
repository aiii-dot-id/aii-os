package id.aiii.shell

// The INBOUND half of the OS wake (MOBILE_PORT §2): AlarmManager fires
// the one next-due alarm AppRuntime scheduled, and we hand the moment
// to Go — TimeWake's catch-up fires EVERYTHING due. OS wake is
// opportunistic acceleration; the durable row + catch-up is the
// correctness floor (TIME_V2 §2.5): late, never lost.
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class WakeReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        // goAsync, for two reasons: onReceive runs on the main thread
        // under an ANR budget while start+timeWake cross into Go — and,
        // the part that matters on a dozing phone, goAsync keeps the
        // system's wakelock held until finish(), so the CPU stays up
        // long enough for the catch-up to land. No wakelock of our own:
        // goAsync's grace (~10s) is the budget; runtime boot is ~1-2s
        // and TIME's evaluation is milliseconds.
        //
        // COLD START IS THE POINT (Sol P1-2): when this broadcast
        // relaunched a dead process, the wake itself is the reason the
        // runtime must start — the alarm receiver window is also the
        // background-start exemption that lets any resulting foreground
        // hold begin legally.
        val result = goAsync()
        AppRuntime.wakeWithRuntime(context) { result.finish() }
    }
}
