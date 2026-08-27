package id.aiii.shell

// The Android shell (MOBILE_PORT §8). Bug hunt 2026-08-18 (#2): the
// runtime is a PROCESS singleton owned by AppRuntime — the activity is
// a window onto it. Rotation/recreation must never double-start (the
// orphan kept the port; the new start died EADDRINUSE) and never stop
// a runtime it does not own.
import android.app.Activity
import android.os.Bundle
import android.webkit.WebView
import mobile.Mobile

class MainActivity : Activity() {
    private lateinit var web: WebView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        web = WebView(this)
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        setContentView(web)
        AppRuntime.ensureStarted(this) { url, err ->
            runOnUiThread {
                if (!isDestroyed && !isFinishing) {
                    if (url != null) { AppRuntime.rt?.setForeground(!isPaused); web.loadUrl(url) }
                    else web.loadData("<h2>start failed</h2><pre>" + (err ?: "") + "</pre>", "text/html", "utf-8")
                }
            }
        }
        AppRuntime.url?.let { web.loadUrl(it) } // recreated activity reuses the live runtime
    }

    private var isPaused = false
    override fun onResume() { super.onResume(); isPaused = false; AppRuntime.rt?.setForeground(true) }
    override fun onPause() { super.onPause(); isPaused = true; AppRuntime.rt?.setForeground(false) }
    // No stop on destroy: the runtime is the identity, the activity is a
    // window. It dies with the process.
}
