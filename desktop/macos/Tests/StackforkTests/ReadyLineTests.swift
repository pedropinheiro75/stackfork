import Foundation
import Testing
@testable import Stackfork

@Suite struct ReadyLineTests {
    @Test func validLine() {
        guard case .ready(let line) = parseReadyLine(readyJSON) else { Issue.record("not ready"); return }
        #expect(line.port == 41873)
        #expect(line.url == "http://127.0.0.1:41873/")
        #expect(line.token.count == 64)
        #expect(line.version == "0.9.0")
    }

    @Test func schemaTwoIsAFailedStart() {
        let line = readyJSON.replacingOccurrences(of: "\"schema\":1", with: "\"schema\":2")
        #expect(parseReadyLine(line) == .rejected("unsupported ready line schema"))
    }

    @Test func schemaOneWithAnotherHostIsRejected() {
        let line = readyJSON.replacingOccurrences(of: "127.0.0.1", with: "example.com")
        #expect(parseReadyLine(line) == .rejected("malformed ready line"))
    }

    @Test func linesThatAreNotTheReadyLine() {
        #expect(parseReadyLine("not json") == .other)
        #expect(parseReadyLine("") == .other)
        #expect(parseReadyLine(#"{"event":"other","schema":1}"#) == .other)
        #expect(parseReadyLine("stackfork dashboard listening on http://127.0.0.1:1") == .other)
    }

    @Test func outputBeforeTheReadyLineIsKeptAndSkipped() {
        var buffer = LineBuffer()
        let lines = buffer.append(Data(("warning: x\nnote\n" + readyJSON + "\n").utf8))
        #expect(lines.count == 3)
        let found = lines.compactMap { line -> ReadyLine? in
            if case .ready(let r) = parseReadyLine(line) { return r } else { return nil }
        }
        #expect(found.count == 1)
    }

    @Test func aLineSplitAcrossTwoReads() {
        var buffer = LineBuffer()
        let bytes = Array((readyJSON + "\n").utf8)
        let cut = bytes.count / 2
        #expect(buffer.append(Data(bytes[..<cut])).isEmpty)
        let lines = buffer.append(Data(bytes[cut...]))
        #expect(lines.count == 1)
        guard case .ready = parseReadyLine(lines[0]) else { Issue.record("not ready"); return }
    }

    @Test func unterminatedRestIsFlushed() {
        var buffer = LineBuffer()
        _ = buffer.append(Data("abc".utf8))
        #expect(buffer.flush() == "abc")
        #expect(buffer.flush() == nil)
    }

    @Test func diagnosticsKeepsTheLast64KiBAndNoTokenFromTheReadyLine() {
        var diagnostics = DiagnosticsBuffer()
        for i in 0..<5000 { diagnostics.append(line: "line \(i) " + String(repeating: "x", count: 40)) }
        #expect(diagnostics.text.utf8.count <= DiagnosticsBuffer.limit)
        #expect(diagnostics.text.contains("line 4999"))
        #expect(!diagnostics.text.contains("line 0 "))
        #expect(diagnostics.lastLines(2).split(separator: "\n").count == 2)
    }
}
