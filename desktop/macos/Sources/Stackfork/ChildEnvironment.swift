import Foundation

/// The environment of the dashboard child (D6): the app's own environment plus
/// the few variables stackfork and its tools read, taken from the person's
/// login shell. Never the whole login environment: the dashboard must not see
/// shell secrets.
enum ChildEnvironment {
    static let allowlist: Set<String> = [
        "PATH", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "STACKFORK_CONFIG_DIR",
        "STACKFORK_PORT_RANGE_START", "STACKFORK_PORT_RANGE_END", "STACKFORK_PROFILE_DIRS",
        "STACKFORK_PROXY_NAME", "LANG", "SSH_AUTH_SOCK",
    ]
    static let beginMarker = "__STACKFORK_ENV_BEGIN__"
    static let endMarker = "__STACKFORK_ENV_END__"
    static let shellScript = "printf '%s\\n' \(beginMarker); env; printf '%s\\n' \(endMarker)"

    struct Result: Equatable, Sendable {
        var environment: [String: String]
        var usedDefaultPath: Bool
    }

    /// The KEY=VALUE lines between the markers; nil when a marker is missing.
    /// Noise before the first marker (profile banners) is ignored.
    static func parse(_ output: String) -> [String: String]? {
        let lines = output.components(separatedBy: "\n").map { $0.trimmingCharacters(in: .init(charactersIn: "\r")) }
        guard let begin = lines.firstIndex(of: beginMarker),
              let end = lines[(begin + 1)...].firstIndex(of: endMarker)
        else { return nil }
        var env: [String: String] = [:]
        for line in lines[(begin + 1)..<end] {
            guard let eq = line.firstIndex(of: "="), eq != line.startIndex else { continue }
            let key = String(line[line.startIndex..<eq])
            guard key.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "_" }) else { continue }
            env[key] = String(line[line.index(after: eq)...])
        }
        return env
    }

    static func filtered(_ env: [String: String]) -> [String: String] {
        env.filter { allowlist.contains($0.key) }
    }

    static func fallbackPath(appPath: String?, home: String) -> String {
        let base = (appPath ?? "").split(separator: ":").map(String.init)
        let extra = ["/opt/homebrew/bin", "/usr/local/bin", home + "/.orbstack/bin",
                     "/Applications/Docker.app/Contents/Resources/bin"]
        return (base + extra).joined(separator: ":")
    }

    /// Builds the child's environment from the app's own and a login-shell
    /// result (nil when the shell failed, timed out or printed no markers).
    static func merge(base: [String: String], shell: [String: String]?, home: String) -> Result {
        var env = base
        if let shell, let path = shell["PATH"], !path.isEmpty {
            for (key, value) in filtered(shell) { env[key] = value }
            return Result(environment: env, usedDefaultPath: false)
        }
        env["PATH"] = fallbackPath(appPath: base["PATH"], home: home)
        return Result(environment: env, usedDefaultPath: true)
    }

    /// Runs `shell -l -i -c SCRIPT` with stdin from /dev/null and returns the
    /// parsed environment, or nil on failure or after `timeout` seconds. Blocks
    /// the calling thread: call it off the main thread.
    static func readLoginShell(shell: String, timeout: TimeInterval = 5) -> [String: String]? {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: shell)
        process.arguments = ["-l", "-i", "-c", shellScript]
        process.standardInput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        let pipe = Pipe()
        process.standardOutput = pipe
        let collector = Collector()
        let eof = DispatchSemaphore(value: 0)
        pipe.fileHandleForReading.readabilityHandler = { handle in
            let chunk = handle.availableData
            if chunk.isEmpty {
                handle.readabilityHandler = nil
                eof.signal()
            } else {
                collector.append(chunk)
            }
        }
        let exited = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in exited.signal() }
        do { try process.run() } catch { return nil }
        if exited.wait(timeout: .now() + timeout) == .timedOut {
            kill(process.processIdentifier, SIGKILL)
            pipe.fileHandleForReading.readabilityHandler = nil
            return nil
        }
        // A background process of the profile may keep the pipe open: wait
        // briefly for the end of file, not for ever.
        _ = eof.wait(timeout: .now() + 0.5)
        pipe.fileHandleForReading.readabilityHandler = nil
        return parse(String(decoding: collector.data, as: UTF8.self))
    }

    /// Reads the login shell (off the main thread) and merges.
    static func resolve(base: [String: String] = ProcessInfo.processInfo.environment,
                        shell: String? = ProcessInfo.processInfo.environment["SHELL"],
                        home: String = NSHomeDirectory(), timeout: TimeInterval = 5) -> Result {
        let parsed = shell.flatMap { readLoginShell(shell: $0, timeout: timeout) }
        return merge(base: base, shell: parsed, home: home)
    }

    private final class Collector: @unchecked Sendable {
        private let lock = NSLock()
        private var storage = Data()
        func append(_ chunk: Data) { lock.lock(); storage.append(chunk); lock.unlock() }
        var data: Data { lock.lock(); defer { lock.unlock() }; return storage }
    }
}
