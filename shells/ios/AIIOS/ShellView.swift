import SwiftUI
import WebKit

struct ShellView: View {
    @ObservedObject var runtime: RuntimeHolder

    var body: some View {
        ZStack {
            WebShell(runtime: runtime)
                .opacity(runtime.rt == nil ? 0 : 1)
                .allowsHitTesting(runtime.rt != nil)
                .accessibilityHidden(runtime.rt == nil)
        if let msg = runtime.startError {
            VStack(spacing: 12) {
                Text("AII OS could not start")
                    .font(.headline)
                Text(msg)
                    .font(.system(.footnote, design: .monospaced))
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 24)
                if runtime.state.retry {
                    Button("Retry") { runtime.retry() }
                }
            }
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(red: 0.024, green: 0.024, blue: 0.055))
        } else if runtime.rt == nil {
            VStack(spacing: 12) {
                ProgressView()
                Text("AII OS is starting or restarting.")
                Text("Waiting for the previous runtime to finish before opening your identity.")
                    .font(.footnote)
            }.padding()
        }
        }
    }
}

struct WebShell: UIViewRepresentable {
    @ObservedObject var runtime: RuntimeHolder

    func makeCoordinator() -> DashboardAuth { DashboardAuth(runtime: runtime) }

    func makeUIView(context: Context) -> WKWebView {
        let cfg = WKWebViewConfiguration()
        cfg.userContentController.addScriptMessageHandler(context.coordinator, contentWorld: .page, name: "aiiDashboardAuth")
        cfg.defaultWebpagePreferences.allowsContentJavaScript = true
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.isOpaque = false
        web.backgroundColor = UIColor(red: 0.024, green: 0.024, blue: 0.055, alpha: 1)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {
        if let url = runtime.url, !sameOrigin(web.url, url) {
            web.load(URLRequest(url: url))
        }
    }

    private func sameOrigin(_ old: URL?, _ next: URL) -> Bool {
        guard let old = old else { return false }
        return old.scheme == next.scheme && old.host == next.host && old.port == next.port
    }

    static func dismantleUIView(_ web: WKWebView, coordinator: DashboardAuth) {
        web.stopLoading()
        web.configuration.userContentController.removeScriptMessageHandler(forName: "aiiDashboardAuth", contentWorld: .page)
    }
}

final class DashboardAuth: NSObject, WKScriptMessageHandlerWithReply {
    private let runtime: RuntimeHolder
    init(runtime: RuntimeHolder) { self.runtime = runtime }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage,
                               replyHandler: @escaping (Any?, String?) -> Void) {
        guard message.frameInfo.isMainFrame, message.body as? String == "token", let rt = runtime.rt else {
            replyHandler(nil, "dashboard sign-in refused"); return
        }
        let origin = message.frameInfo.securityOrigin
        var url = URLComponents()
        url.scheme = origin.protocol
        url.host = origin.host
        if origin.port > 0 { url.port = origin.port }
        guard let source = url.string else { replyHandler(nil, "dashboard sign-in refused"); return }
        var error: NSError?
        let token = rt.dashboardAccessToken(forOrigin: source, error: &error)
        if error == nil && !token.isEmpty { replyHandler(token, nil) }
        else { replyHandler(nil, "dashboard sign-in refused") }
    }
}
