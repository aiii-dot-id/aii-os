package id.aiii.shell

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class WakeReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val result = goAsync()
        AppRuntime.wakeWithRuntime(context) { result.finish() }
    }
}
