import Foundation

/// The one JSON line `stackfork dashboard --supervised` prints once it listens
/// (add-macos-app D5). Schema 1 has exactly these keys.
struct ReadyLine: Decodable, Equatable, Sendable {
    let event: String
    let schema: Int
    let url: String
    let port: Int
    let token: String
    let pid: Int
    let version: String
}

enum ReadyLineResult: Equatable, Sendable {
    case ready(ReadyLine)
    /// A ready line of another schema, or a schema 1 line that is incomplete
    /// or does not point at 127.0.0.1: a failed start (the app and its helper
    /// come from one build, so this is a packaging bug).
    case rejected(String)
    /// Any other line (not JSON, or another event).
    case other
}

func parseReadyLine(_ line: String) -> ReadyLineResult {
    guard let data = line.data(using: .utf8),
          let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
          object["event"] as? String == "ready"
    else { return .other }
    guard object["schema"] as? Int == 1 else {
        return .rejected("unsupported ready line schema")
    }
    guard let ready = try? JSONDecoder().decode(ReadyLine.self, from: data),
          let url = URL(string: ready.url), url.scheme == "http", url.host == "127.0.0.1",
          url.port == ready.port, !ready.token.isEmpty
    else { return .rejected("malformed ready line") }
    return .ready(ready)
}

/// Splits a byte stream into lines across reads.
struct LineBuffer: Sendable {
    private var pending = Data()

    mutating func append(_ chunk: Data) -> [String] {
        pending.append(chunk)
        var lines: [String] = []
        while let newline = pending.firstIndex(of: 0x0A) {
            lines.append(String(decoding: pending[pending.startIndex..<newline], as: UTF8.self))
            pending.removeSubrange(pending.startIndex...newline)
        }
        return lines
    }

    /// The unterminated rest, at end of stream.
    mutating func flush() -> String? {
        defer { pending.removeAll() }
        return pending.isEmpty ? nil : String(decoding: pending, as: UTF8.self)
    }
}

/// The last 64 KiB of the child's output, in memory only (D7). The ready line
/// (it carries the token) is never added.
struct DiagnosticsBuffer: Sendable {
    static let limit = 64 * 1024
    private var data = Data()

    mutating func append(line: String) {
        data.append(contentsOf: Array((line + "\n").utf8))
        if data.count > Self.limit { data.removeFirst(data.count - Self.limit) }
    }

    var text: String { String(decoding: data, as: UTF8.self) }

    func lastLines(_ count: Int) -> String {
        text.split(separator: "\n", omittingEmptySubsequences: true).suffix(count).joined(separator: "\n")
    }
}
