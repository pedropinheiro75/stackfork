import AppKit
import WebKit

enum NavigationDecision: Equatable {
    case allow
    case openExternally
    case cancel
}

/// A main-frame navigation stays in the web view only when its scheme, host and
/// port equal the dashboard URL's. Any other http(s) URL opens in the default
/// browser; any other scheme is cancelled (D9).
func navigationDecision(for url: URL?, dashboard: URL?) -> NavigationDecision {
    guard let url, let scheme = url.scheme?.lowercased() else { return .cancel }
    guard scheme == "http" || scheme == "https" else { return .cancel }
    func port(_ url: URL, _ scheme: String) -> Int { url.port ?? (scheme == "https" ? 443 : 80) }
    if let dashboard, let dashboardScheme = dashboard.scheme?.lowercased(),
       scheme == dashboardScheme,
       url.host?.lowercased() == dashboard.host?.lowercased(),
       port(url, scheme) == port(dashboard, dashboardScheme) {
        return .allow
    }
    return .openExternally
}

/// One window with a web view on the dashboard page. Closing hides it; the app
/// keeps running. No script message handler and no injected script: the page
/// cannot call into the app (D9).
@MainActor
final class DashboardWindow: NSObject, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    let window: NSWindow
    let webView: WKWebView
    private let placeholder = NSTextField(labelWithString: "Starting dashboard…")
    private(set) var dashboardURL: URL?

    static func makeConfiguration() -> WKWebViewConfiguration {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default() // the page's localStorage survives launches
        return configuration
    }

    override init() {
        webView = WKWebView(frame: .zero, configuration: Self.makeConfiguration())
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 780),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        super.init()
        window.title = "Stackfork"
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.center()
        window.setFrameAutosaveName("StackforkDashboard")

        let content = NSView()
        webView.translatesAutoresizingMaskIntoConstraints = false
        webView.isHidden = true
        webView.navigationDelegate = self
        webView.uiDelegate = self
        placeholder.translatesAutoresizingMaskIntoConstraints = false
        placeholder.font = .systemFont(ofSize: 16)
        placeholder.textColor = .secondaryLabelColor
        content.addSubview(webView)
        content.addSubview(placeholder)
        NSLayoutConstraint.activate([
            webView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            webView.topAnchor.constraint(equalTo: content.topAnchor),
            webView.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            placeholder.centerXAnchor.constraint(equalTo: content.centerXAnchor),
            placeholder.centerYAnchor.constraint(equalTo: content.centerYAnchor),
        ])
        window.contentView = content
    }

    /// Loads the page of a new ready line.
    func load(_ url: URL) {
        dashboardURL = url
        placeholder.isHidden = true
        webView.isHidden = false
        webView.load(URLRequest(url: url))
    }

    /// Dock icon and Command-Tab while the window is visible; none after.
    func show() {
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    func windowWillClose(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
    }

    // MARK: Navigation

    private func route(_ url: URL?, mainFrame: Bool) -> WKNavigationActionPolicy {
        switch navigationDecision(for: url, dashboard: dashboardURL) {
        case .allow:
            return .allow
        case .openExternally:
            if mainFrame, let url { NSWorkspace.shared.open(url) }
            return .cancel
        case .cancel:
            return .cancel
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async -> WKNavigationActionPolicy {
        route(navigationAction.request.url, mainFrame: navigationAction.targetFrame?.isMainFrame ?? true)
    }

    /// `target="_blank"` and `window.open`: the same rule, and never a new web view.
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        _ = route(navigationAction.request.url, mainFrame: true)
        return nil
    }

    // MARK: JavaScript panels, as sheets

    private func sheet(_ message: String, buttons: [String], accessory: NSView? = nil) async -> NSApplication.ModalResponse {
        let alert = NSAlert()
        alert.messageText = message
        for title in buttons { alert.addButton(withTitle: title) }
        alert.accessoryView = accessory
        if !window.isVisible { show() }
        return await alert.beginSheetModal(for: window)
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async {
        _ = await sheet(message, buttons: ["OK"])
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async -> Bool {
        await sheet(message, buttons: ["OK", "Cancel"]) == .alertFirstButtonReturn
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo) async -> String? {
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 24))
        field.stringValue = defaultText ?? ""
        let response = await sheet(prompt, buttons: ["OK", "Cancel"], accessory: field)
        return response == .alertFirstButtonReturn ? field.stringValue : nil
    }
}
