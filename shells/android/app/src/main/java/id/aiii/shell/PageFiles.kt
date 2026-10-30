package id.aiii.shell

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.webkit.CookieManager
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.widget.Toast
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

class PageFiles(private val activity: Activity, private val web: WebView, private val origin: String) {
    private var choosing: ValueCallback<Array<Uri>>? = null
    private var saving: Pending? = null

    private class Pending(val name: String, val bytes: ByteArray?, val url: String?)

    fun attach() {
        web.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                choosing?.onReceiveValue(null)
                choosing = callback
                return try {
                    activity.startActivityForResult(params.createIntent(), CHOOSE); true
                } catch (_: Exception) {
                    choosing = null; callback.onReceiveValue(null); false
                }
            }
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(web, BRIDGE, setOf(origin)) { _, message, _, isMainFrame, _ ->
                val blob = if (isMainFrame && message.type == WebMessageCompat.TYPE_STRING) blobMessage(message.data ?: "") else null
                if (blob != null) save(Pending(blob.first, blob.second, null))
            }
        }
        web.setDownloadListener { url, _, disposition, mime, _ ->
            when (kindOf(url, origin)) {
                Kind.BLOB -> web.evaluateJavascript(blobScript(url), null)
                Kind.PAGE -> save(Pending(fileName(url, disposition, mime), null, url))
                Kind.OTHER -> toast("A download from outside this dashboard was not taken.")
            }
        }
    }

    fun detach() {
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.removeWebMessageListener(web, BRIDGE)
        }
        web.setDownloadListener(null)
    }

    private fun save(p: Pending) {
        saving = p
        val pick = Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
            .setType("application/octet-stream").putExtra(Intent.EXTRA_TITLE, p.name)
        try { activity.startActivityForResult(pick, SAVE) }
        catch (_: Exception) { saving = null; toast("This device offers no place to save a file, so ${p.name} was not saved.") }
    }

    fun onResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        when (requestCode) {
            CHOOSE -> {
                choosing?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data))
                choosing = null
            }
            SAVE -> {
                val p = saving; saving = null
                val target = data?.data
                if (p == null || resultCode != Activity.RESULT_OK || target == null) {
                    if (p != null) toast("${p.name} was not saved.")
                    return true
                }
                Thread {
                    val said = try { write(p, target); "${p.name} is saved." }
                    catch (e: Exception) { "${p.name} was not saved: ${e.message ?: e.javaClass.simpleName}." }
                    activity.runOnUiThread { toast(said) }
                }.start()
            }
            else -> return false
        }
        return true
    }

    private fun write(p: Pending, target: Uri) {
        val out = activity.contentResolver.openOutputStream(target) ?: error("the place picked cannot be written")
        out.use { sink ->
            if (p.bytes != null) { sink.write(p.bytes); return }
            fetch(p.url!!).use { it.copyTo(sink) }
        }
    }

    private fun fetch(url: String): InputStream {
        fun open(method: String): HttpURLConnection = (URL(url).openConnection() as HttpURLConnection).apply {
            requestMethod = method
            instanceFollowRedirects = false
            CookieManager.getInstance().getCookie(url)?.let { setRequestProperty("Cookie", it) }
            if (method == "POST") {
                setRequestProperty("Origin", origin)
                doOutput = true
                outputStream.close()
            }
        }
        var c = open("GET")
        if (c.responseCode == HttpURLConnection.HTTP_BAD_METHOD) { c.disconnect(); c = open("POST") }
        val code = c.responseCode
        if (code != HttpURLConnection.HTTP_OK) {
            val why = c.errorStream?.bufferedReader()?.use { it.readText().trim() }.orEmpty()
            c.disconnect()
            error(if (why.isEmpty()) "the dashboard answered $code" else why)
        }
        return c.inputStream
    }

    private fun toast(s: String) = Toast.makeText(activity, s, Toast.LENGTH_LONG).show()

    enum class Kind { BLOB, PAGE, OTHER }

    companion object {
        const val BRIDGE = "aiiPageFile"
        private const val CHOOSE = 4101
        private const val SAVE = 4102

        fun kindOf(url: String, origin: String): Kind = when {
            url.startsWith("blob:$origin/") -> Kind.BLOB
            url == origin || url.startsWith("$origin/") -> Kind.PAGE
            else -> Kind.OTHER
        }

        fun fileName(url: String, disposition: String?, mime: String?): String =
            safeName(URLUtil.guessFileName(url, disposition, mime))

        fun safeName(name: String): String {
            val base = name.substringAfterLast('/').substringAfterLast('\\').filter { it >= ' ' && it != ':' }.trim()
            return if (base.isEmpty() || base == "." || base == "..") "download" else base.take(120)
        }

        fun blobMessage(s: String): Pair<String, ByteArray>? {
            val nl = s.indexOf('\n')
            if (nl <= 0) return null
            val data = s.substring(nl + 1)
            val comma = data.indexOf(',')
            if (!data.startsWith("data:") || comma < 0 || !data.substring(0, comma).endsWith(";base64")) return null
            return try { Pair(safeName(s.substring(0, nl)), java.util.Base64.getDecoder().decode(data.substring(comma + 1))) }
            catch (_: IllegalArgumentException) { null }
        }

        fun blobScript(url: String): String {
            val u = org.json.JSONObject.quote(url)
            return "(async()=>{try{const u=$u;const a=[...document.querySelectorAll('a[download]')].find(x=>x.href===u);" +
                "const b=await (await fetch(u)).blob();const r=new FileReader();" +
                "r.onload=()=>$BRIDGE.postMessage(((a&&a.download)||'download')+'\\n'+r.result);r.readAsDataURL(b);}catch(e){}})()"
        }
    }
}
