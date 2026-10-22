import Testing
@testable import Stackfork

@MainActor
@Suite struct HotKeyTests {
    final class Fake: HotKeyRegistering {
        var error: String?
        var fire: (@MainActor () -> Void)?
        func register(_ handler: @escaping @MainActor () -> Void) -> String? { fire = handler; return error }
    }

    @Test func handlerShowsTheWindow() {
        let fake = Fake()
        var shown = 0
        let shortcut = DashboardShortcut(registrar: fake, show: { shown += 1 }, log: { _ in })
        fake.fire?()
        #expect(shortcut.active)
        #expect(shown == 1)
    }

    @Test func failureIsLoggedAndInactive() {
        let fake = Fake()
        fake.error = "taken"
        var logged: [String] = []
        let shortcut = DashboardShortcut(registrar: fake, show: {}, log: { logged.append($0) })
        #expect(!shortcut.active)
        #expect(logged == ["shortcut: taken"])
    }

    @Test func registeringTheSameCombinationTwiceReportsAnError() {
        let first = GlobalHotKey(), second = GlobalHotKey()
        // If another app owns the combination, the first call fails and there is nothing to compare.
        if first.register({}) == nil { #expect(second.register({}) != nil) }
    }
}
