import Foundation

/// What the menu shows, as plain values (D10). The AppKit layer only draws it.
struct MenuModel: Equatable {
    enum RowAction: Equatable { case start, stop, openDashboard }

    struct Row: Equatable {
        let project: String
        let branch: String
        let glyph: String
        let stateWord: String
        /// `Starting…` or `Stopping…` while an action the app sent runs.
        let busy: String?
        let actions: [RowAction]
        var title: String { "\(glyph) \(branch)  \(busy ?? stateWord)" }
        var actionsEnabled: Bool { busy == nil }
    }

    struct Group: Equatable {
        let app: String
        let rows: [Row]
    }

    enum List: Equatable {
        case none
        case dockerDown
        case groups([Group], more: Int)
    }

    static let rowLimit = 25
    static let dockerDownText = "Docker is not running"

    let statusText: String
    let list: List
    let showRestart: Bool
    let showLog: Bool

    static func make(state: Supervisor.State, now: TimeInterval, usedDefaultPath: Bool,
                     inventory: InstancesResponse?, pending: [String: InstanceAction]) -> MenuModel {
        var status: String
        switch state {
        case .idle, .starting: status = "Starting…"
        case .ready: status = "Dashboard running"
        case .restarting(let at): status = "Restarting in \(max(1, Int((at - now).rounded(.up)))) s"
        case .stopped(let reason): status = "Stopped: \(reason)"
        case .quitting: status = "Quitting…"
        }
        if usedDefaultPath { status += " (using a default PATH)" }

        var list = List.none
        if state == .ready, let inventory {
            list = inventory.docker.reachable ? groups(inventory.instances, pending: pending) : .dockerDown
        }
        var showRestart = false, showLog = false
        switch state {
        case .stopped: showRestart = true; showLog = true
        case .restarting: showLog = true
        default: break
        }
        return MenuModel(statusText: status, list: list, showRestart: showRestart, showLog: showLog)
    }

    // MARK: Grouping, sorted as the page sorts (app.js groupProjects)

    static func appName(_ instance: InstancesResponse.Instance) -> String {
        if let app = instance.app, !app.isEmpty { return app }
        return displayName(instance.project)
    }

    static func displayName(_ project: String) -> String {
        var name = project
        for prefix in ["sf-", "idv-"] where name.hasPrefix(prefix) { name.removeFirst(prefix.count); break }
        if let range = name.range(of: "-[0-9a-f]{12}$", options: .regularExpression) { name.removeSubrange(range) }
        return name.isEmpty ? project : name
    }

    static func isLive(_ instance: InstancesResponse.Instance) -> Bool {
        instance.state == "running" || instance.state == "partial"
    }

    private static func milliseconds(_ text: String?) -> Double {
        guard let text else { return 0 }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        return ((fractional.date(from: text) ?? plain.date(from: text))?.timeIntervalSince1970 ?? 0) * 1000
    }

    static func groups(_ instances: [InstancesResponse.Instance], pending: [String: InstanceAction]) -> List {
        var order: [String] = []
        var byApp: [String: [InstancesResponse.Instance]] = [:]
        for instance in instances {
            let app = appName(instance)
            if byApp[app] == nil { order.append(app) }
            byApp[app, default: []].append(instance)
        }
        func last(_ app: String) -> Double { byApp[app]!.map { milliseconds($0.used) }.max() ?? 0 }
        func live(_ app: String) -> Bool { byApp[app]!.contains(where: isLive) }
        let apps = order.sorted { a, b in
            if live(a) != live(b) { return live(a) }
            if last(a) != last(b) { return last(a) > last(b) }
            return a < b
        }
        var shown = 0, total = 0
        var groups: [Group] = []
        for app in apps {
            let sorted = byApp[app]!.sorted { a, b in
                if isLive(a) != isLive(b) { return isLive(a) }
                if (a.isMain ?? false) != (b.isMain ?? false) { return a.isMain ?? false }
                if (a.branch ?? "") != (b.branch ?? "") { return (a.branch ?? "") < (b.branch ?? "") }
                return a.project < b.project
            }
            total += sorted.count
            var rows: [Row] = []
            for instance in sorted where shown < rowLimit {
                rows.append(row(instance, pending: pending[instance.project]))
                shown += 1
            }
            if !rows.isEmpty { groups.append(Group(app: app, rows: rows)) }
        }
        return .groups(groups, more: total - shown)
    }

    static func row(_ instance: InstancesResponse.Instance, pending: InstanceAction?) -> Row {
        let (glyph, word, actions): (String, String, [RowAction])
        switch instance.state {
        case "running": (glyph, word, actions) = ("●", "Running", [.stop, .openDashboard])
        case "partial": (glyph, word, actions) = ("◐", "Partial", [.start, .stop, .openDashboard])
        case "stopped": (glyph, word, actions) = ("○", "Stopped", [.start, .openDashboard])
        case "waiting": (glyph, word, actions) = ("◌", "Waiting", [.openDashboard])
        default: (glyph, word, actions) = ("◇", "Unknown", [.openDashboard])
        }
        let busy = pending.map { $0 == .up ? "Starting…" : "Stopping…" }
        let branch = (instance.branch ?? "").isEmpty ? instance.project : instance.branch!
        return Row(project: instance.project, branch: branch, glyph: glyph, stateWord: word, busy: busy, actions: actions)
    }
}

/// Fetches while the menu is open: at once, then every `interval` seconds; no
/// request while it is closed (D10).
@MainActor
final class MenuPoller {
    private let scheduler: any Scheduler
    private let interval: TimeInterval
    private let fetch: @MainActor () -> Void
    private var timer: ScheduledTask?
    private var isOpen = false

    init(scheduler: any Scheduler, interval: TimeInterval = 5, fetch: @escaping @MainActor () -> Void) {
        self.scheduler = scheduler
        self.interval = interval
        self.fetch = fetch
    }

    func menuOpened() {
        guard !isOpen else { return }
        isOpen = true
        tick()
    }

    func menuClosed() {
        isOpen = false
        timer?.cancel()
        timer = nil
    }

    private func tick() {
        guard isOpen else { return }
        fetch()
        timer = scheduler.schedule(after: interval) { [weak self] in self?.tick() }
    }
}

/// Open at Login needs a stable path (D11): a quarantined app launched from
/// the DMG or Downloads runs from a translocated, read-only path.
enum InstallLocation {
    static func allowsLoginItem(bundlePath: String, home: String) -> Bool {
        let path = URL(fileURLWithPath: bundlePath).standardizedFileURL.path
        guard !path.contains("/AppTranslocation/") else { return false }
        let homeApps = URL(fileURLWithPath: home).standardizedFileURL.path + "/Applications/"
        return path.hasPrefix("/Applications/") || path.hasPrefix(homeApps)
    }
}
