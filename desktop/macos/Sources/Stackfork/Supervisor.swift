import Foundation

/// Owns the dashboard child (add-macos-app D7): start, readiness, restart with
/// backoff, quit. Everything runs on the main actor; child callbacks hop here.
@MainActor
final class Supervisor {
    struct Config: Sendable {
        var readyTimeout: TimeInterval = 30
        var backoff: [TimeInterval] = [1, 2, 4, 8, 16]
        var stableAfter: TimeInterval = 60
        var killGrace: TimeInterval = 5
    }

    enum State: Equatable {
        case idle
        case starting
        case ready
        case restarting(at: TimeInterval)
        case stopped(reason: String)
        case quitting
    }

    private(set) var state: State = .idle
    private(set) var ready: ReadyLine?
    private(set) var diagnostics = DiagnosticsBuffer()
    /// Running actions reported by the child while it shuts down.
    private(set) var waitingActions: Int?

    var onChange: (() -> Void)?
    var onReady: ((ReadyLine) -> Void)?
    var onWaiting: ((Int) -> Void)?

    private let launcher: any ChildLauncher
    private let scheduler: any Scheduler
    private let config: Config
    private var child: (any ChildProcess)?
    private var generation = 0
    private var failures = 0
    private var readyAt: TimeInterval?
    private var readyTimer: ScheduledTask?
    private var restartTimer: ScheduledTask?
    private var killTimer: ScheduledTask?
    private var failureReason: String?
    private var secondTermSent = false
    private var quitCompletion: (() -> Void)?

    init(launcher: any ChildLauncher, scheduler: any Scheduler, config: Config = Config()) {
        self.launcher = launcher
        self.scheduler = scheduler
        self.config = config
    }

    var isChildRunning: Bool { child != nil }

    /// Closes the child's standard input, as the kernel does when the app dies.
    /// For the orphan test only.
    func closeChildInput() { child?.closeStdin() }

    /// Starts the child; also the manual **Restart Dashboard** (resets the count).
    func start() {
        guard child == nil, state != .quitting else { return }
        restartTimer?.cancel()
        failures = 0
        spawn()
    }

    private func spawn() {
        generation += 1
        let gen = generation
        ready = nil
        readyAt = nil
        failureReason = nil
        waitingActions = nil
        secondTermSent = false
        state = .starting
        let events = ChildEvents(
            stdout: { line in onMain { [weak self] in self?.handleStdout(line, gen) } },
            stderr: { line in onMain { [weak self] in self?.handleStderr(line, gen) } },
            exited: { status in onMain { [weak self] in self?.handleExit(status, gen) } })
        do {
            child = try launcher.launch(events)
        } catch {
            child = nil
            registerFailure(reason: "cannot start the dashboard: \(error.localizedDescription)", livedLong: false)
            return
        }
        readyTimer = scheduler.schedule(after: config.readyTimeout) { [weak self] in
            self?.readyTimedOut(gen)
        }
        onChange?()
    }

    private func readyTimedOut(_ gen: Int) {
        guard gen == generation, ready == nil, state == .starting else { return }
        failureReason = "no ready line within \(Int(config.readyTimeout)) seconds"
        terminateChild(gen)
    }

    /// SIGTERM now, SIGKILL after the grace period; the exit counts as a failed start.
    private func terminateChild(_ gen: Int) {
        child?.send(signal: SIGTERM)
        killTimer?.cancel()
        killTimer = scheduler.schedule(after: config.killGrace) { [weak self] in
            guard let self, gen == self.generation else { return }
            self.child?.send(signal: SIGKILL)
        }
    }

    private func handleStdout(_ line: String, _ gen: Int) {
        guard gen == generation else { return }
        let parsed = parseReadyLine(line)
        if ready == nil, state == .starting {
            switch parsed {
            case .ready(let info):
                ready = info
                readyAt = scheduler.now()
                readyTimer?.cancel()
                state = .ready
                onChange?()
                onReady?(info)
                return
            case .rejected(let reason):
                failureReason = reason
                readyTimer?.cancel()
                terminateChild(gen)
                return
            case .other:
                break
            }
        }
        // A ready line carries the token: it never reaches the buffer, whatever the state.
        guard parsed == .other else { return }
        diagnostics.append(line: line)
        if state == .quitting, let count = Self.waitingCount(in: line) {
            waitingActions = count
            onWaiting?(count)
        }
    }

    private func handleStderr(_ line: String, _ gen: Int) {
        guard gen == generation else { return }
        diagnostics.append(line: line)
    }

    private func handleExit(_ status: Int32, _ gen: Int) {
        guard gen == generation else { return }
        readyTimer?.cancel()
        killTimer?.cancel()
        child = nil
        let wasReady = ready != nil
        let lived = readyAt.map { scheduler.now() - $0 }
        ready = nil
        if state == .quitting {
            let done = quitCompletion
            quitCompletion = nil
            onChange?()
            done?()
            return
        }
        let reason = failureReason ?? diagnostics.lastLines(1).nonEmpty
            ?? (wasReady ? "the dashboard exited (status \(status))" : "the dashboard exited before it was ready (status \(status))")
        registerFailure(reason: reason, livedLong: (lived ?? 0) > config.stableAfter)
    }

    private func registerFailure(reason: String, livedLong: Bool) {
        let delay: TimeInterval
        if livedLong {
            failures = 0 // it ran: not a failed start
            delay = config.backoff[0]
        } else {
            failures += 1
            guard failures <= config.backoff.count else {
                state = .stopped(reason: reason)
                onChange?()
                return
            }
            delay = config.backoff[failures - 1]
        }
        state = .restarting(at: scheduler.now() + delay)
        onChange?()
        restartTimer = scheduler.schedule(after: delay) { [weak self] in
            guard let self, case .restarting = self.state else { return }
            self.spawn()
        }
    }

    // MARK: Quit (D7)

    /// SIGTERM to the child and wait. `completion` runs after the child exited
    /// (at once when there is none). Without a `waiting for N running action(s)`
    /// line five seconds after the SIGTERM the child is stuck, not busy: the
    /// escalation of **Quit Now** runs by itself.
    func quit(completion: @escaping () -> Void) {
        restartTimer?.cancel()
        readyTimer?.cancel()
        state = .quitting
        quitCompletion = completion
        guard let current = child else {
            quitCompletion = nil
            completion()
            return
        }
        let gen = generation
        current.send(signal: SIGTERM)
        onChange?()
        killTimer?.cancel()
        killTimer = scheduler.schedule(after: config.killGrace) { [weak self] in
            guard let self, gen == self.generation, self.waitingActions == nil else { return }
            self.quitNow()
        }
    }

    /// The second SIGTERM (the dashboard's forced exit), then SIGKILL after the grace period.
    func quitNow() {
        guard state == .quitting, let current = child, !secondTermSent else { return }
        secondTermSent = true
        let gen = generation
        current.send(signal: SIGTERM)
        killTimer?.cancel()
        killTimer = scheduler.schedule(after: config.killGrace) { [weak self] in
            guard let self, gen == self.generation else { return }
            self.child?.send(signal: SIGKILL)
        }
    }

    static func waitingCount(in line: String) -> Int? {
        guard let range = line.range(of: #"waiting for (\d+) running action"#, options: .regularExpression) else { return nil }
        return Int(line[range].filter(\.isNumber))
    }
}

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
