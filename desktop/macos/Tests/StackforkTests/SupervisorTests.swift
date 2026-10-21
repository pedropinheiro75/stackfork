import Foundation
import Testing
@testable import Stackfork

@MainActor
@Suite struct SupervisorTests {
    func make() -> (Supervisor, FakeLauncher, ManualScheduler) {
        let launcher = FakeLauncher(), clock = ManualScheduler()
        return (Supervisor(launcher: launcher, scheduler: clock), launcher, clock)
    }

    @Test func readyLineMakesTheChildReadyAndStoresTheTokenOnlyInReady() {
        let (supervisor, launcher, _) = make()
        supervisor.start()
        launcher.last.print("noise before")
        launcher.last.print(readyJSON)
        #expect(supervisor.state == .ready)
        #expect(supervisor.ready?.port == 41873)
        #expect(supervisor.diagnostics.text == "noise before\n")
        #expect(!supervisor.diagnostics.text.contains("token"))
    }

    @Test func aChildThatExitsAtOnceIsStartedSixTimesWithTheBackoffThenStopped() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        var startedAt: [TimeInterval] = [clock.now()]
        for _ in 0..<5 {
            launcher.last.exit()
            guard case .restarting = supervisor.state else { Issue.record("not restarting"); return }
            let before = launcher.children.count
            while launcher.children.count == before { clock.advance(by: 1) }
            startedAt.append(clock.now())
        }
        launcher.last.exit()
        #expect(launcher.children.count == 6)
        #expect(zip(startedAt, startedAt.dropFirst()).map { $1 - $0 } == [1, 2, 4, 8, 16])
        guard case .stopped = supervisor.state else { Issue.record("not stopped: \(supervisor.state)"); return }
        clock.advance(by: 100)
        #expect(launcher.children.count == 6)
        supervisor.start() // Restart Dashboard
        #expect(launcher.children.count == 7)
        #expect(supervisor.state == .starting)
    }

    @Test func aChildThatRanLongerThanSixtySecondsResetsTheCount() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        // two quick failures
        launcher.last.exit(); clock.advance(by: 1)
        launcher.last.exit(); clock.advance(by: 2)
        #expect(launcher.children.count == 3)
        launcher.last.print(readyJSON)
        clock.advance(by: 61)
        launcher.last.exit()
        guard case .restarting(let at) = supervisor.state else { Issue.record("not restarting"); return }
        #expect(at - clock.now() == 1)
        clock.advance(by: 1)
        #expect(launcher.children.count == 4)
        // the next quick failure starts again from 1 s
        launcher.last.exit()
        guard case .restarting(let next) = supervisor.state else { Issue.record("not restarting"); return }
        #expect(next - clock.now() == 1)
    }

    @Test func anExitSoonAfterReadyCountsAsAFailure() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        launcher.last.print(readyJSON)
        clock.advance(by: 59)
        launcher.last.exit()
        launcher.last.print("x") // stale child output is ignored
        guard case .restarting(let at) = supervisor.state else { Issue.record("not restarting"); return }
        #expect(at - clock.now() == 1)
        clock.advance(by: 1)
        launcher.last.exit()
        guard case .restarting(let second) = supervisor.state else { Issue.record("not restarting"); return }
        #expect(second - clock.now() == 2)
    }

    @Test func aChildThatNeverPrintsTheReadyLineIsKilledAtThirtySeconds() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        clock.advance(by: 29.9)
        #expect(launcher.last.signals.isEmpty)
        clock.advance(by: 0.2)
        #expect(launcher.last.signals == [SIGTERM])
        clock.advance(by: 5)
        #expect(launcher.last.signals == [SIGTERM, SIGKILL])
        launcher.last.exit(137)
        guard case .restarting = supervisor.state else { Issue.record("not restarting"); return }
    }

    @Test func aReadyLineOfAnotherSchemaIsAFailedStart() {
        let (supervisor, launcher, _) = make()
        supervisor.start()
        launcher.last.print(readyJSON.replacingOccurrences(of: "\"schema\":1", with: "\"schema\":2"))
        #expect(supervisor.state == .starting)
        #expect(launcher.last.signals == [SIGTERM])
        launcher.last.exit(143)
        guard case .restarting = supervisor.state else { Issue.record("not restarting"); return }
    }

    @Test func quitWhenIdleDoesNotWaitForAChild() {
        let (supervisor, _, _) = make()
        var done = false
        supervisor.quit { done = true }
        #expect(done)
    }

    @Test func quitSendsOneSigtermAndCompletesWhenTheChildExits() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        launcher.last.print(readyJSON)
        var done = false
        supervisor.quit { done = true }
        #expect(launcher.last.signals == [SIGTERM])
        #expect(!done)
        launcher.last.exit(0)
        #expect(done)
        clock.advance(by: 60)
        #expect(launcher.last.signals == [SIGTERM]) // no SIGKILL, no restart
        #expect(launcher.children.count == 1)
    }

    @Test func quitReportsRunningActionsAndQuitNowEscalates() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        launcher.last.print(readyJSON)
        var counts: [Int] = []
        supervisor.onWaiting = { counts.append($0) }
        supervisor.quit {}
        launcher.last.print("waiting for 2 running action(s) to finish (signal again to stop now)")
        #expect(counts == [2])
        clock.advance(by: 30) // busy, not stuck: no automatic escalation
        #expect(launcher.last.signals == [SIGTERM])
        supervisor.quitNow()
        #expect(launcher.last.signals == [SIGTERM, SIGTERM])
        clock.advance(by: 4.9)
        #expect(launcher.last.signals == [SIGTERM, SIGTERM])
        clock.advance(by: 0.2)
        #expect(launcher.last.signals == [SIGTERM, SIGTERM, SIGKILL])
    }

    @Test func aStuckChildWithNoRunningActionsIsEscalatedByItself() {
        let (supervisor, launcher, clock) = make()
        supervisor.start()
        launcher.last.print(readyJSON)
        supervisor.quit {}
        clock.advance(by: 5.1)
        #expect(launcher.last.signals == [SIGTERM, SIGTERM])
        clock.advance(by: 5.1)
        #expect(launcher.last.signals == [SIGTERM, SIGTERM, SIGKILL])
    }

    @Test func waitingCountParsing() {
        #expect(Supervisor.waitingCount(in: "waiting for 3 running action(s) to finish (signal again to stop now)") == 3)
        #expect(Supervisor.waitingCount(in: "hello") == nil)
    }
}
