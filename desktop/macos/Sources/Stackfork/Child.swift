import Foundation

/// Callbacks of one child process. They run on background threads.
struct ChildEvents: Sendable {
    var stdout: @Sendable (String) -> Void
    var stderr: @Sendable (String) -> Void
    /// Called once, after the output is drained: exit status, or the signal.
    var exited: @Sendable (Int32) -> Void
}

protocol ChildProcess: Sendable {
    var pid: Int32 { get }
    func send(signal: Int32)
    /// Closes the write end of standard input: the child sees end of file.
    /// What the kernel does when the app dies; only tests call it.
    func closeStdin()
}

protocol ChildLauncher: Sendable {
    func launch(_ events: ChildEvents) throws -> any ChildProcess
}

/// Runs a real process with a pipe on standard input that the app keeps open
/// and never writes (the child's parent watch, D4), and pipes on output.
final class ProcessLauncher: ChildLauncher, @unchecked Sendable {
    private let executable: URL
    private let arguments: [String]
    private let environment: [String: String]

    init(executable: URL, arguments: [String], environment: [String: String]) {
        self.executable = executable
        self.arguments = arguments
        self.environment = environment
    }

    func launch(_ events: ChildEvents) throws -> any ChildProcess {
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.environment = environment
        let input = Pipe(), output = Pipe(), errors = Pipe()
        process.standardInput = input
        process.standardOutput = output
        process.standardError = errors
        let drained = DispatchGroup()
        let pumps = [LinePump(handle: output.fileHandleForReading, group: drained, sink: events.stdout),
                     LinePump(handle: errors.fileHandleForReading, group: drained, sink: events.stderr)]
        process.terminationHandler = { finished in
            let status = finished.terminationStatus
            DispatchQueue.global().async {
                _ = drained.wait(timeout: .now() + 1)
                _ = pumps
                events.exited(status)
            }
        }
        try process.run()
        // The write end stays open in this object for as long as the child
        // runs; the kernel closes it if the app dies, which ends the child.
        return RunningChild(process: process, stdin: input.fileHandleForWriting)
    }
}

private final class RunningChild: ChildProcess, @unchecked Sendable {
    private let process: Process
    private let stdin: FileHandle // held open on purpose
    init(process: Process, stdin: FileHandle) { self.process = process; self.stdin = stdin }
    var pid: Int32 { process.processIdentifier }
    func closeStdin() { try? stdin.close() }
    func send(signal: Int32) {
        // Only the child's own process id, never a group (D7).
        if process.isRunning { kill(process.processIdentifier, signal) }
    }
}

/// Reads a pipe and hands out whole lines.
private final class LinePump: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = LineBuffer()
    private let sink: @Sendable (String) -> Void

    init(handle: FileHandle, group: DispatchGroup, sink: @escaping @Sendable (String) -> Void) {
        self.sink = sink
        group.enter()
        handle.readabilityHandler = { [self] handle in
            let chunk = handle.availableData
            if chunk.isEmpty {
                handle.readabilityHandler = nil
                lock.lock(); let rest = buffer.flush(); lock.unlock()
                if let rest { sink(rest) }
                group.leave()
                return
            }
            lock.lock(); let lines = buffer.append(chunk); lock.unlock()
            for line in lines { sink(line) }
        }
    }
}
