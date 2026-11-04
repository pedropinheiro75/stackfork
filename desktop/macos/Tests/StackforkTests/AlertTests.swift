import AppKit
import Foundation
import Testing
@testable import Stackfork

@MainActor
final class FakePresenter: AlertPresenting {
    private(set) var shown: [AlertSpec] = []
    private(set) var tokens: [AlertToken] = []
    private(set) var open = 0
    private var completions: [@MainActor (Int) -> Void] = []

    func present(_ spec: AlertSpec, completion: @escaping @MainActor (Int) -> Void) -> AlertToken {
        shown.append(spec)
        completions.append(completion)
        open += 1
        let token = AlertToken { [weak self] in self?.open -= 1 }
        tokens.append(token)
        return token
    }

    func click(_ button: Int, alert: Int = 0) {
        tokens[alert].close()
        completions[alert](button)
    }
}

@MainActor
@Suite struct AlertTests {
    @Test func quitDuringAStartClosesTheSheetWhenTheChildExitsWithoutAClick() {
        let launcher = FakeLauncher(), clock = ManualScheduler(), presenter = FakePresenter()
        let supervisor = Supervisor(launcher: launcher, scheduler: clock)
        supervisor.start()
        launcher.last.print(readyJSON)
        var finished = 0
        let quit = QuitCoordinator(supervisor: supervisor, presenter: presenter) { finished += 1 }
        quit.begin()
        launcher.last.print("waiting for 1 running action(s) to finish (signal again to stop now)")
        #expect(presenter.shown.map(\.buttons) == [["Quit Now"]])
        #expect(presenter.open == 1)
        // the action ends and the child exits while the sheet is open
        launcher.last.exit(0)
        #expect(finished == 1)
        #expect(presenter.open == 0, "the sheet must close by itself")
    }

    @Test func quitNowFromTheSheetSendsTheSecondSigterm() {
        let launcher = FakeLauncher(), clock = ManualScheduler(), presenter = FakePresenter()
        let supervisor = Supervisor(launcher: launcher, scheduler: clock)
        supervisor.start()
        launcher.last.print(readyJSON)
        let quit = QuitCoordinator(supervisor: supervisor, presenter: presenter) {}
        quit.begin()
        launcher.last.print("waiting for 2 running action(s) to finish (signal again to stop now)")
        presenter.click(0)
        #expect(launcher.last.signals == [SIGTERM, SIGTERM])
    }

    @Test func anOpenFailureAlertDoesNotStopRestartsOrTheReadyTimeout() {
        let launcher = FakeLauncher(), clock = ManualScheduler(), presenter = FakePresenter()
        let supervisor = Supervisor(launcher: launcher, scheduler: clock)
        supervisor.start()
        _ = presenter.present(AlertSpec(message: "Start failed", buttons: ["OK"])) { _ in }
        launcher.last.exit()
        clock.advance(by: 1)
        #expect(launcher.children.count == 2)
        clock.advance(by: 31)
        #expect(launcher.last.signals.first == SIGTERM)
    }

    /// The real presenter: timers and main-actor tasks keep running while an
    /// alert is open. A modal loop entered from a main-queue job stops them.
    @Test func theMainQueueKeepsRunningWhileARealAlertIsOpen() async {
        _ = NSApplication.shared
        let presenter = PanelAlertPresenter()
        var clicked: Int?
        let token = presenter.present(AlertSpec(message: "Waiting", buttons: ["Quit Now"])) { clicked = $0 }
        var timerFired = false
        let timer = DispatchScheduler().schedule(after: 0.2) { timerFired = true }
        var taskRan = false
        Task { @MainActor in taskRan = true }
        // A modal loop blocks forever, so a long wait still catches it. The first
        // window can stall the main thread for seconds on a CI runner (16 s seen).
        let ok = await eventually(timeout: 30) { timerFired && taskRan }
        #expect(ok)
        timer.cancel()
        token.close()
        #expect(clicked == nil)
    }
}
