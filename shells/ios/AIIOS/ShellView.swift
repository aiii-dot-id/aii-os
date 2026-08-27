import SwiftUI
import WebKit

// The shell is a window onto the runtime. When there is no runtime it
// must say so: an empty WebView is indistinguishable from a dark theme,
// and the operator cannot tell a booting identity from a broken one.
struct ShellView: View {
    @ObservedObject var runtime: RuntimeHolder

    var body: some View {
        if let msg = runtime.startError {
            VStack(spacing: 12) {
                Text("AII OS could not start")
                    .font(.headline)
                Text(msg)
                    .font(.system(.footnote, design: .monospaced))
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 24)
            }
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(red: 0.024, green: 0.024, blue: 0.055))
        } else {
            WebShell(runtime: runtime)
        }
    }
}

struct WebShell: UIViewRepresentable {
    @ObservedObject var runtime: RuntimeHolder

    func makeUIView(context: Context) -> WKWebView {
        let cfg = WKWebViewConfiguration()
        cfg.defaultWebpagePreferences.allowsContentJavaScript = true
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.isOpaque = false
        web.backgroundColor = UIColor(red: 0.024, green: 0.024, blue: 0.055, alpha: 1)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {
        if let url = runtime.url, web.url == nil {
            web.load(URLRequest(url: url))
        }
    }
}
