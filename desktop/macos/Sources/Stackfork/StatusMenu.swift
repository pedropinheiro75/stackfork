import AppKit
import ServiceManagement

enum LoginItemState: Equatable {
    case on, off, requiresApproval
    /// The bundle is not in /Applications or ~/Applications.
    case wrongLocation
}

/// Open at Login is the system's state only (D11): the app stores nothing.
@MainActor
enum LoginItem {
    static func state() -> LoginItemState {
        guard InstallLocation.allowsLoginItem(bundlePath: Bundle.main.bundlePath, home: NSHomeDirectory()) else {
            return .wrongLocation
        }
        switch SMAppService.mainApp.status {
        case .enabled: return .on
        case .requiresApproval: return .requiresApproval
        default: return .off
        }
    }

    static func toggle() throws {
        let service = SMAppService.mainApp
        if service.status == .enabled || service.status == .requiresApproval {
            try service.unregister()
        } else {
            try service.register()
        }
    }

    static func openSettings() { SMAppService.openSystemSettingsLoginItems() }
}

/// Draws a `MenuModel` as an `NSMenu`. It is rebuilt when the menu opens and
/// again when a response arrives while it is open.
@MainActor
final class StatusMenu: NSObject, NSMenuDelegate {
    struct Handlers {
        var model: () -> MenuModel
        var loginState: () -> LoginItemState
        var opened: () -> Void
        var closed: () -> Void
        var openDashboard: () -> Void
        var run: (InstanceAction, String) -> Void
        var toggleLogin: () -> Void
        var approveLogin: () -> Void
        var restart: () -> Void
        var showLog: () -> Void
        var quit: () -> Void
    }

    let statusItem: NSStatusItem
    let menu = NSMenu()
    private let handlers: Handlers
    private(set) var isOpen = false

    private final class Box: NSObject {
        let run: () -> Void
        init(_ run: @escaping () -> Void) { self.run = run }
    }

    init(handlers: Handlers) {
        self.handlers = handlers
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        super.init()
        let image = NSImage(systemSymbolName: "square.stack.3d.up", accessibilityDescription: "Stackfork")
        image?.isTemplate = true // follows the menu bar's light and dark appearance
        statusItem.button?.image = image
        menu.delegate = self
        menu.autoenablesItems = false
        statusItem.menu = menu
    }

    func rebuildIfOpen() { if isOpen { rebuild() } }

    func menuNeedsUpdate(_ menu: NSMenu) { rebuild() }
    func menuWillOpen(_ menu: NSMenu) { isOpen = true; handlers.opened() }
    func menuDidClose(_ menu: NSMenu) { isOpen = false; handlers.closed() }

    @objc private func fire(_ sender: NSMenuItem) { (sender.representedObject as? Box)?.run() }

    private func item(_ title: String, enabled: Bool = true, state: NSControl.StateValue = .off,
                      key: String = "", action: (() -> Void)? = nil) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action == nil ? nil : #selector(fire(_:)), keyEquivalent: key)
        item.target = self
        item.isEnabled = enabled && action != nil
        item.state = state
        if let action { item.representedObject = Box(action) }
        return item
    }

    private func rebuild() {
        let model = handlers.model()
        menu.removeAllItems()
        menu.addItem(item(model.statusText))
        menu.addItem(item("Open Dashboard", action: handlers.openDashboard))
        menu.addItem(.separator())

        switch model.list {
        case .none:
            break
        case .dockerDown:
            menu.addItem(item(MenuModel.dockerDownText))
            menu.addItem(.separator())
        case .groups(let groups, let more):
            for group in groups {
                let header = item(group.app)
                header.attributedTitle = NSAttributedString(string: group.app, attributes: [.font: NSFont.boldSystemFont(ofSize: 13)])
                menu.addItem(header)
                for row in group.rows { menu.addItem(rowItem(row)) }
            }
            if more > 0 { menu.addItem(item("\(more) more in the dashboard…", action: handlers.openDashboard)) }
            if !groups.isEmpty || more > 0 { menu.addItem(.separator()) }
        }

        switch handlers.loginState() {
        case .on:
            menu.addItem(item("Open at Login", state: .on, action: handlers.toggleLogin))
        case .off:
            menu.addItem(item("Open at Login", action: handlers.toggleLogin))
        case .requiresApproval:
            menu.addItem(item("Open at Login", state: .on, action: handlers.toggleLogin))
            menu.addItem(item("Approve in System Settings…", action: handlers.approveLogin))
        case .wrongLocation:
            menu.addItem(item("Move Stackfork to Applications to open it at login", enabled: false))
        }
        if model.showRestart { menu.addItem(item("Restart Dashboard", action: handlers.restart)) }
        if model.showLog { menu.addItem(item("Show Dashboard Log…", action: handlers.showLog)) }
        menu.addItem(item("Quit Stackfork", key: "q", action: handlers.quit))
    }

    private func rowItem(_ row: MenuModel.Row) -> NSMenuItem {
        let parent = item(row.title)
        parent.isEnabled = true
        let submenu = NSMenu()
        submenu.autoenablesItems = false
        for action in row.actions {
            switch action {
            case .start:
                submenu.addItem(item("Start", enabled: row.actionsEnabled) { [handlers] in handlers.run(.up, row.project) })
            case .stop:
                submenu.addItem(item("Stop", enabled: row.actionsEnabled) { [handlers] in handlers.run(.down, row.project) })
            case .openDashboard:
                submenu.addItem(item("Open Dashboard", action: handlers.openDashboard))
            }
        }
        parent.submenu = submenu
        return parent
    }
}
