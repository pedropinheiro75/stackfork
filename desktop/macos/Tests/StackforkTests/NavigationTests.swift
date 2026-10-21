import Foundation
import Testing
import WebKit
@testable import Stackfork

@Suite struct NavigationTests {
    let dashboard = URL(string: "http://127.0.0.1:41873/")!

    func decide(_ url: String) -> NavigationDecision {
        navigationDecision(for: URL(string: url), dashboard: dashboard)
    }

    @Test func sameOriginStays() {
        #expect(decide("http://127.0.0.1:41873/") == .allow)
        #expect(decide("http://127.0.0.1:41873/api/instances") == .allow)
        #expect(decide("HTTP://127.0.0.1:41873/x?y=1#z") == .allow)
    }

    @Test func otherHttpOriginsOpenInTheBrowser() {
        #expect(decide("http://127.0.0.1:41874/") == .openExternally)
        #expect(decide("http://stackfork.localhost/") == .openExternally)
        #expect(decide("http://acme-web.localhost:8080/") == .openExternally)
        #expect(decide("https://github.com/") == .openExternally)
        #expect(decide("https://127.0.0.1:41873/") == .openExternally)
        #expect(decide("http://localhost:41873/") == .openExternally)
    }

    @Test func otherSchemesAreCancelled() {
        #expect(decide("file:///etc/hosts") == .cancel)
        #expect(decide("javascript:alert(1)") == .cancel)
        #expect(decide("about:blank") == .cancel)
        #expect(decide("x-apple.systempreferences:com.apple.LoginItems") == .cancel)
        #expect(navigationDecision(for: nil, dashboard: dashboard) == .cancel)
    }

    @Test func beforeTheFirstReadyLineNothingStays() {
        #expect(navigationDecision(for: URL(string: "http://127.0.0.1:41873/"), dashboard: nil) == .openExternally)
    }

    @MainActor
    @Test func theWebViewHasNoUserScriptsAndNoMessageHandlers() throws {
        let configuration = DashboardWindow.makeConfiguration()
        #expect(configuration.userContentController.userScripts.isEmpty)
        for url in try Sources.all() {
            let text = try String(contentsOf: url, encoding: .utf8)
            #expect(!text.contains("add(self, name"), "\(url.lastPathComponent)")
            #expect(!text.contains("ScriptMessageHandler"), "\(url.lastPathComponent)")
            #expect(!text.contains("WKUserScript"), "\(url.lastPathComponent)")
            #expect(!text.contains("evaluateJavaScript"), "\(url.lastPathComponent)")
        }
    }

    @Test func theAppAcceptsNoCommandsFromOtherProcesses() throws {
        // no URL scheme, no scripting dictionary, no XPC service
        let plist = try String(contentsOf: Sources.dir.deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("Info.plist"), encoding: .utf8)
        #expect(!plist.contains("CFBundleURLTypes"))
        #expect(!plist.contains("OSAScriptingDefinition"))
        #expect(!plist.contains("NSAppleScriptEnabled"))
        #expect(!plist.contains("XPCService"))
    }
}
