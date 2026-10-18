package id.aiii.shell

import android.app.Activity
import android.os.Bundle
import android.webkit.WebView
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView

class MainActivity : Activity() {
    private lateinit var web: WebView
    private var attachedOrigin: String? = null
    private val changed: (AppRuntime.State) -> Unit = { state -> show(state) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        web = WebView(this)
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        AppRuntime.observe(changed)
        AppRuntime.ensureStarted(this) { _, _ -> }
    }

    private fun show(state: AppRuntime.State) {
        if (isDestroyed || isFinishing || state !== AppRuntime.state) return
        val runtime = state.runtime
        if (runtime != null) {
            val url = android.net.Uri.parse(runtime.dashboardURL())
            val origin = "${url.scheme}://${url.encodedAuthority}"
            if (attachedOrigin != origin) {
                DashboardAuth.detach(web)
                if (!DashboardAuth.attach(web, runtime) { AppRuntime.rt }) {
                    notice("Update Android System WebView to open AII OS securely.", false)
                    return
                }
                attachedOrigin = origin
                web.loadUrl(runtime.dashboardURL())
            }
            (web.parent as? android.view.ViewGroup)?.removeView(web)
            setContentView(web)
        } else {
            notice(state.error ?: "AII OS is starting or restarting. Waiting for the previous runtime to finish before opening your identity.", state.retry)
        }
    }

    private fun notice(message: String, retry: Boolean) {
        val box = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val space = (16 * resources.displayMetrics.density).toInt()
        box.setOnApplyWindowInsetsListener { view, insets ->
            val top: Int
            val bottom: Int
            if (android.os.Build.VERSION.SDK_INT >= 30) {
                val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars())
                top = bars.top; bottom = bars.bottom
            } else {
                @Suppress("DEPRECATION")
                top = insets.systemWindowInsetTop
                @Suppress("DEPRECATION")
                bottom = insets.systemWindowInsetBottom
            }
            view.setPadding(space, top + (actionBar?.height ?: 0) + space, space, bottom + space)
            insets
        }
        box.addView(TextView(this).apply { text = message })
        if (retry) box.addView(Button(this).apply {
            text = "Retry"
            setOnClickListener { AppRuntime.retry(this@MainActivity) }
        })
        setContentView(box)
        box.requestApplyInsets()
    }

    override fun onResume() { super.onResume(); AppRuntime.setForeground(true) }
    override fun onPause() { super.onPause(); AppRuntime.setForeground(false) }
    override fun onDestroy() {
        AppRuntime.detach(changed)
        DashboardAuth.detach(web)
        web.destroy()
        super.onDestroy()
    }
}
