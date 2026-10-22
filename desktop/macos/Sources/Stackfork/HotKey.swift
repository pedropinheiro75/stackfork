import Carbon.HIToolbox

/// A system-wide shortcut. Carbon's `RegisterEventHotKey` needs no Accessibility permission.
@MainActor
protocol HotKeyRegistering {
    /// Returns an error text when the shortcut cannot be registered (taken by another app).
    func register(_ handler: @escaping @MainActor () -> Void) -> String?
}

/// Control-Option-Command-S: shows the dashboard even when the notch hides the status item.
@MainActor
final class GlobalHotKey: HotKeyRegistering {
    static let label = "⌃⌥⌘S"
    private static var handler: (@MainActor () -> Void)?
    private var ref: EventHotKeyRef?

    // ponytail: one hot key, so one static handler; no id table.
    func register(_ handler: @escaping @MainActor () -> Void) -> String? {
        Self.handler = handler
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let installed = InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
            DispatchQueue.main.async { MainActor.assumeIsolated { GlobalHotKey.handler?() } }
            return noErr
        }, 1, &spec, nil, nil)
        guard installed == noErr else { return "event handler failed (\(installed))" }
        let id = EventHotKeyID(signature: OSType(0x7366_6b79), id: 1) // "sfky"
        let status = RegisterEventHotKey(UInt32(kVK_ANSI_S), UInt32(controlKey | optionKey | cmdKey), id,
                                         GetApplicationEventTarget(), 0, &ref)
        return status == noErr ? nil : "\(Self.label) is not available (error \(status)); another app may use it"
    }
}

/// Registers the shortcut and reports whether it works. A failure goes to `log` only.
@MainActor
struct DashboardShortcut {
    private(set) var active = false

    init(registrar: any HotKeyRegistering, show: @escaping @MainActor () -> Void, log: (String) -> Void) {
        if let error = registrar.register(show) {
            log("shortcut: \(error)")
        } else {
            active = true
        }
    }
}
