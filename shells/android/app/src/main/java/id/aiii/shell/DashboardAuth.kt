package id.aiii.shell

import android.net.Uri
import android.webkit.WebView
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import androidx.webkit.WebMessageCompat

object DashboardAuth {
    fun detach(web: WebView) {
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.removeWebMessageListener(web, "aiiDashboardAuth")
        }
    }
    internal fun isTokenRequest(mainFrame: Boolean, message: WebMessageCompat): Boolean =
        mainFrame && message.type == WebMessageCompat.TYPE_STRING && message.data == "token"

    fun attach(web: WebView, runtime: mobile.Runtime, current: () -> mobile.Runtime? = { runtime }): Boolean {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) return false
        val url = Uri.parse(runtime.dashboardURL())
        val origin = "${url.scheme}://${url.encodedAuthority}"
        WebViewCompat.addWebMessageListener(web, "aiiDashboardAuth", setOf(origin)) {
                _, message, sourceOrigin, isMainFrame, reply ->
            var token = ""
            if (isTokenRequest(isMainFrame, message)) {
                try { token = current()?.dashboardAccessTokenForOrigin(sourceOrigin.toString()) ?: "" }
                catch (_: Exception) { }
            }
            reply.postMessage(token)
        }
        return true
    }
}
