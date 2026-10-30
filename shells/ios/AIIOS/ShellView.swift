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

    func makeCoordinator() -> ShellCoordinator { ShellCoordinator(runtime: runtime) }

    func makeUIView(context: Context) -> WKWebView {
        let cfg = WKWebViewConfiguration()
        cfg.userContentController.addScriptMessageHandler(context.coordinator.auth, contentWorld: .page, name: "aiiDashboardAuth")
        cfg.defaultWebpagePreferences.allowsContentJavaScript = true
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.isOpaque = false
        web.backgroundColor = UIColor(red: 0.024, green: 0.024, blue: 0.055, alpha: 1)
        web.navigationDelegate = context.coordinator.files
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

    static func dismantleUIView(_ web: WKWebView, coordinator: ShellCoordinator) {
        web.stopLoading()
        web.configuration.userContentController.removeScriptMessageHandler(forName: "aiiDashboardAuth", contentWorld: .page)
    }
}

final class ShellCoordinator {
    let auth: DashboardAuth
    let files: PageFiles
    init(runtime: RuntimeHolder) {
        auth = DashboardAuth(runtime: runtime)
        files = PageFiles(runtime: runtime)
    }
}

final class PageFiles: NSObject, WKNavigationDelegate, WKDownloadDelegate, UIDocumentPickerDelegate {
    private let runtime: RuntimeHolder
    private var taken: [ObjectIdentifier: URL] = [:]
    private var exporting: [ObjectIdentifier: URL] = [:]
    init(runtime: RuntimeHolder) { self.runtime = runtime }

    private func ours(_ url: URL?) -> Bool {
        guard let url = url, let home = runtime.url else { return false }
        if url.scheme == "blob", let inner = URL(string: String(url.absoluteString.dropFirst("blob:".count))) {
            return inner.scheme == home.scheme && inner.host == home.host && inner.port == home.port
        }
        return url.scheme == home.scheme && url.host == home.host && url.port == home.port
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                 preferences: WKWebpagePreferences,
                 decisionHandler: @escaping (WKNavigationActionPolicy, WKWebpagePreferences) -> Void) {
        decisionHandler(navigationAction.shouldPerformDownload && ours(navigationAction.request.url) ? .download : .allow, preferences)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        let disposition = (navigationResponse.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Disposition") ?? ""
        let attachment = disposition.lowercased().hasPrefix("attachment") || !navigationResponse.canShowMIMEType
        decisionHandler(attachment && ours(navigationResponse.response.url) ? .download : .allow)
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String,
                  completionHandler: @escaping (URL?) -> Void) {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("page-file-" + UUID().uuidString, isDirectory: true)
        do { try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true) }
        catch { completionHandler(nil); return }
        let file = dir.appendingPathComponent(PageFiles.safeName(suggestedFilename))
        taken[ObjectIdentifier(download)] = file
        completionHandler(file)
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let file = taken.removeValue(forKey: ObjectIdentifier(download)) else { return }
        let sheet = UIDocumentPickerViewController(forExporting: [file], asCopy: true)
        sheet.delegate = self
        exporting[ObjectIdentifier(sheet)] = file
        guard let top = PageFiles.topController() else { discard(file); return }
        top.present(sheet, animated: true)
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        if let file = taken.removeValue(forKey: ObjectIdentifier(download)) { discard(file) }
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) { finished(controller) }
    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) { finished(controller) }

    private func finished(_ sheet: UIDocumentPickerViewController) {
        if let file = exporting.removeValue(forKey: ObjectIdentifier(sheet)) { discard(file) }
    }

    private func discard(_ file: URL) {
        try? FileManager.default.removeItem(at: file.deletingLastPathComponent())
    }

    static func safeName(_ name: String) -> String {
        let base = name.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
        let clean = String(base.unicodeScalars.filter { $0.value >= 0x20 && $0 != ":" }.map(Character.init)).trimmingCharacters(in: .whitespaces)
        return clean.isEmpty || clean == "." || clean == ".." ? "download" : String(clean.prefix(120))
    }

    private static func topController() -> UIViewController? {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        var top = scene?.keyWindow?.rootViewController
        while let next = top?.presentedViewController { top = next }
        return top
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
