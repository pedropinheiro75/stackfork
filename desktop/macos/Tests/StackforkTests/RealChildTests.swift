import Foundation
import Testing
@testable import Stackfork

/// Supervisor tests with real processes (small shell scripts) and the real
/// clock, with a short kill grace so they stay quick.
@MainActor
@Suite struct RealChildTests {
    func run(script: String, grace: TimeInterval = 0.5) throws -> (Supervisor, Exit) {
        let url = try makeScript(script)
        let exit = Exit()
        let inner = ProcessLauncher(executable: url, arguments: [], environment: ["PATH": "/usr/bin:/bin"])
        let launcher = RecordingLauncher(inner: inner, exit: exit)
        var config = Supervisor.Config()
        config.killGrace = grace
        return (Supervisor(launcher: launcher, scheduler: DispatchScheduler(), config: config), exit)
    }

    @Test func aChildThatIgnoresSigtermEndsWithSigkill() async throws {
        let (supervisor, exit) = try run(script: "trap '' TERM\nprintf '%s\\n' '\(readyJSON)'\nexec sleep 600")
        supervisor.start()
        #expect(await eventually { supervisor.state == .ready })
        var quit = false
        supervisor.quit { quit = true }
        #expect(await eventually { quit })
        #expect(exit.status == SIGKILL)
    }

    @Test func aChildThatExitsOnSigtermEndsWithoutSigkill() async throws {
        let (supervisor, exit) = try run(script: "trap 'exit 0' TERM\nprintf '%s\\n' '\(readyJSON)'\nwhile :; do sleep 0.1; done")
        supervisor.start()
        #expect(await eventually { supervisor.state == .ready })
        var quit = false
        supervisor.quit { quit = true }
        #expect(await eventually { quit })
        #expect(exit.status == 0)
    }

    @Test func closingTheSupervisorsStdinEndsASupervisedGoChild() async throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        guard let go = Self.findGo(), FileManager.default.fileExists(atPath: root.appendingPathComponent("go.mod").path) else {
            return // skipped: no go toolchain or not in the repository
        }
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("sf-orphan-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let binary = tmp.appendingPathComponent("stackfork")
        let build = Process()
        build.executableURL = URL(fileURLWithPath: go)
        build.arguments = ["build", "-o", binary.path, "./cmd/stackfork"]
        build.currentDirectoryURL = root
        try build.run()
        build.waitUntilExit()
        #expect(build.terminationStatus == 0)

        var env = ProcessInfo.processInfo.environment
        env["STACKFORK_CONFIG_DIR"] = tmp.appendingPathComponent("config").path
        env["STACKFORK_PORT_RANGE_START"] = "47100" // a private window, never a real dashboard's port
        env["STACKFORK_PORT_RANGE_END"] = "47199"
        let launcher = ProcessLauncher(
            executable: binary,
            arguments: ["--state-dir", tmp.appendingPathComponent("state").path,
                        "--legacy-state-dir", tmp.appendingPathComponent("legacy").path, "dashboard", "--supervised"],
            environment: env)
        let supervisor = Supervisor(launcher: launcher, scheduler: DispatchScheduler())
        supervisor.start()
        #expect(await eventually { supervisor.state == .ready })
        supervisor.closeChildInput()
        let restarted = await eventually {
            if case .restarting = supervisor.state { return true } else { return false }
        }
        #expect(restarted) // the child left on its own: end of file on its standard input
        var quit = false
        supervisor.quit { quit = true }
        #expect(await eventually { quit })
    }

    static func findGo() -> String? {
        let paths = (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":").map(String.init)
            + ["/opt/homebrew/bin", "/usr/local/go/bin", "/usr/local/bin"]
        return paths.map { $0 + "/go" }.first { FileManager.default.isExecutableFile(atPath: $0) }
    }
}

final class Exit: @unchecked Sendable {
    private let lock = NSLock()
    private var _status: Int32?
    var status: Int32? { lock.lock(); defer { lock.unlock() }; return _status }
    func set(_ value: Int32) { lock.lock(); _status = value; lock.unlock() }
}

/// Wraps a launcher to remember the exit status of the child.
final class RecordingLauncher: ChildLauncher, @unchecked Sendable {
    let inner: any ChildLauncher
    let exit: Exit
    init(inner: any ChildLauncher, exit: Exit) { self.inner = inner; self.exit = exit }
    func launch(_ events: ChildEvents) throws -> any ChildProcess {
        var wrapped = events
        let exit = self.exit
        wrapped.exited = { status in exit.set(status); events.exited(status) }
        return try inner.launch(wrapped)
    }
}
