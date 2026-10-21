import AppKit

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let scheduler = DispatchScheduler()
    private let window = DashboardWindow()
    private var statusMenu: StatusMenu?
    private var poller: MenuPoller?
    private var supervisor: Supervisor?
    private var api: APIClient?
    private var inventory: InstancesResponse?
    private var pending: [String: InstanceAction] = [:]
    private var usedDefaultPath = false
    private var startupError: String?
    private var quitAlert: NSAlert?
    private var logWindow: NSWindow?

    func applicationDidFinishLaunching(_ notification: Notification) {
        buildMainMenu()
        poller = MenuPoller(scheduler: scheduler) { [weak self] in self?.refreshInventory() }
        statusMenu = StatusMenu(handlers: .init(
            model: { [unowned self] in currentModel() },
            loginState: { LoginItem.state() },
            opened: { [unowned self] in poller?.menuOpened() },
            closed: { [unowned self] in poller?.menuClosed() },
            openDashboard: { [unowned self] in window.show() },
            run: { [unowned self] action, project in run(action, project: project) },
            toggleLogin: { [unowned self] in toggleLogin() },
            approveLogin: { LoginItem.openSettings() },
            restart: { [unowned self] in supervisor?.start() },
            showLog: { [unowned self] in showLog() },
            quit: { NSApp.terminate(nil) }))

        // The login shell read can take up to 5 s: off the main thread.
        Task { [weak self] in
            let result = await Task.detached { ChildEnvironment.resolve() }.value
            self?.startSupervisor(result)
        }
    }

    private func startSupervisor(_ environment: ChildEnvironment.Result) {
        // Always the helper inside this bundle, never a stackfork found on PATH (D3).
        let helper = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/stackfork")
        guard FileManager.default.isExecutableFile(atPath: helper.path) else {
            startupError = "the stackfork helper is missing from the app bundle"
            statusMenu?.rebuildIfOpen()
            return
        }
        usedDefaultPath = environment.usedDefaultPath
        let launcher = ProcessLauncher(executable: helper, arguments: ["dashboard", "--supervised"],
                                       environment: environment.environment)
        let supervisor = Supervisor(launcher: launcher, scheduler: scheduler)
        supervisor.onChange = { [weak self] in self?.statusMenu?.rebuildIfOpen() }
        supervisor.onReady = { [weak self] ready in self?.dashboardReady(ready) }
        supervisor.onWaiting = { [weak self] count in self?.showQuitSheet(count) }
        self.supervisor = supervisor
        supervisor.start()
    }

    private func dashboardReady(_ ready: ReadyLine) {
        guard let endpoint = DashboardEndpoint(ready), let url = URL(string: ready.url) else { return }
        api = APIClient(endpoint: endpoint) // the token lives here, in memory only
        inventory = nil
        pending = [:]
        window.load(url)
        if statusMenu?.isOpen == true { refreshInventory() }
    }

    private func currentModel() -> MenuModel {
        let state = supervisor?.state ?? (startupError.map { .stopped(reason: $0) } ?? .idle)
        return MenuModel.make(state: state, now: scheduler.now(), usedDefaultPath: usedDefaultPath,
                              inventory: inventory, pending: pending)
    }

    // MARK: Inventory and actions

    /// Only called while the menu is open (and once when a response is due).
    private func refreshInventory() {
        guard let api else { return }
        Task { [weak self] in
            let response = try? await api.fetchInstances()
            guard let self, self.api === api else { return }
            if let response { self.inventory = response }
            self.statusMenu?.rebuildIfOpen()
        }
    }

    private func run(_ action: InstanceAction, project: String) {
        guard let api, pending[project] == nil else { return }
        pending[project] = action
        statusMenu?.rebuildIfOpen()
        Task { [weak self] in
            let outcome = await api.perform(action, project: project)
            guard let self else { return }
            self.pending[project] = nil
            self.report(outcome)
            if self.statusMenu?.isOpen == true { self.refreshInventory() }
        }
    }

    private func report(_ outcome: ActionOutcome) {
        switch outcome {
        case .done, .tokenChanged:
            break // a changed token means a new ready line is coming
        case .alreadyRunning:
            alert("An action is already running for this instance.", offerDashboard: false)
        case .failed(let message), .transport(let message):
            alert(message, offerDashboard: true)
        }
    }

    private func alert(_ message: String, offerDashboard: Bool) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        if offerDashboard { alert.addButton(withTitle: "Open Dashboard") }
        NSApp.activate()
        if alert.runModal() == .alertSecondButtonReturn { window.show() }
    }

    private func toggleLogin() {
        do { try LoginItem.toggle() } catch { alert("Open at Login failed: \(error.localizedDescription)", offerDashboard: false) }
        statusMenu?.rebuildIfOpen()
    }

    // MARK: Log window

    private func showLog() {
        let text = supervisor?.diagnostics.text ?? ""
        let scroll = NSTextView.scrollableTextView()
        if let view = scroll.documentView as? NSTextView {
            view.isEditable = false
            view.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
            view.string = text.isEmpty ? "(no output)" : text
        }
        let log = logWindow ?? NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 420),
                                        styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        log.title = "Stackfork Dashboard Log"
        log.isReleasedWhenClosed = false
        log.contentView = scroll
        logWindow = log
        log.center()
        log.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    // MARK: Quit (D7)

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let supervisor, supervisor.state != .quitting else {
            return supervisor == nil ? .terminateNow : .terminateLater
        }
        supervisor.quit { [weak self] in
            if let self, self.quitAlert != nil { NSApp.abortModal() }
            Task { @MainActor in NSApp.reply(toApplicationShouldTerminate: true) } // never inside applicationShouldTerminate
        }
        return .terminateLater
    }

    private func showQuitSheet(_ count: Int) {
        guard quitAlert == nil else { return }
        let alert = NSAlert()
        alert.messageText = "Waiting for \(count) running action(s) to finish"
        alert.informativeText = "Quit Now stops them."
        alert.addButton(withTitle: "Quit Now")
        quitAlert = alert
        Task { [weak self] in
            NSApp.activate()
            if alert.runModal() == .alertFirstButtonReturn { self?.supervisor?.quitNow() }
            self?.quitAlert = nil
        }
    }

    // MARK: Main menu: App, Edit and Window, so Command-C and Command-V work in the web view

    private func buildMainMenu() {
        let main = NSMenu()
        func submenu(_ title: String, _ items: [NSMenuItem]) {
            let parent = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            let menu = NSMenu(title: title)
            items.forEach(menu.addItem)
            parent.submenu = menu
            main.addItem(parent)
        }
        func entry(_ title: String, _ action: Selector, _ key: String, _ modifiers: NSEvent.ModifierFlags = .command) -> NSMenuItem {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
            item.keyEquivalentModifierMask = modifiers
            return item
        }
        submenu("Stackfork", [entry("Quit Stackfork", #selector(NSApplication.terminate(_:)), "q")])
        submenu("Edit", [
            entry("Undo", Selector(("undo:")), "z"),
            entry("Redo", Selector(("redo:")), "z", [.command, .shift]),
            .separator(),
            entry("Cut", #selector(NSText.cut(_:)), "x"),
            entry("Copy", #selector(NSText.copy(_:)), "c"),
            entry("Paste", #selector(NSText.paste(_:)), "v"),
            entry("Select All", #selector(NSText.selectAll(_:)), "a"),
        ])
        submenu("Window", [
            entry("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"),
            entry("Close", #selector(NSWindow.performClose(_:)), "w"),
        ])
        NSApp.mainMenu = main
    }
}
