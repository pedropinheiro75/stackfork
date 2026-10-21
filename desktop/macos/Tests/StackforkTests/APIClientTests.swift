import Foundation
import Testing
@testable import Stackfork

@Suite(.serialized) struct APIClientTests {
    func client() -> APIClient {
        guard case .ready(let line) = parseReadyLine(readyJSON), let endpoint = DashboardEndpoint(line) else { fatalError() }
        return APIClient(endpoint: endpoint, session: FakeURLProtocol.session())
    }

    @Test func instancesIsAGetWithTheDecodedInventory() async throws {
        FakeURLProtocol.reset { _ in (200, Data(instancesJSON.utf8)) }
        let response = try await client().fetchInstances()
        #expect(response.docker.reachable)
        #expect(response.instances.count == 5)
        let request = try #require(FakeURLProtocol.recorded.first)
        #expect(request.method == "GET")
        #expect(request.path == "/api/instances")
        #expect(FakeURLProtocol.recorded.count == 1)
    }

    @Test func instancesToleratesNullList() async throws {
        FakeURLProtocol.reset { _ in (200, Data(#"{"docker":{"reachable":false,"error":"x"},"instances":null}"#.utf8)) }
        let response = try await client().fetchInstances()
        #expect(!response.docker.reachable)
        #expect(response.instances.isEmpty)
    }

    @Test func startIsAPostWithTheThreeHeaders() async throws {
        FakeURLProtocol.reset { _ in (200, Data(#"{"ok":true,"message":"started"}"#.utf8)) }
        let outcome = await client().perform(.up, project: "sf-acme-web-main-0123456789ab")
        #expect(outcome == .done("started"))
        let request = try #require(FakeURLProtocol.recorded.first)
        #expect(request.method == "POST")
        #expect(request.path == "/api/actions")
        #expect(request.headers["Content-Type"] == "application/json")
        #expect(request.headers["X-Stackfork-Token"] == String(repeating: "ab", count: 32))
        #expect(request.headers["Origin"] == "http://127.0.0.1:41873")
        let body = try #require(try JSONSerialization.jsonObject(with: request.body) as? [String: String])
        #expect(body == ["action": "up", "project": "sf-acme-web-main-0123456789ab"])
    }

    @Test func stopSendsDown() async throws {
        FakeURLProtocol.reset { _ in (200, Data(#"{"ok":true,"message":"stopped"}"#.utf8)) }
        _ = await client().perform(.down, project: "p")
        let body = try #require(try JSONSerialization.jsonObject(with: FakeURLProtocol.recorded[0].body) as? [String: String])
        #expect(body["action"] == "down")
    }

    @Test func outcomes() async {
        FakeURLProtocol.reset { _ in (409, Data(#"{"ok":false,"message":"an action is already running for this instance"}"#.utf8)) }
        #expect(await client().perform(.up, project: "p") == .alreadyRunning)
        FakeURLProtocol.reset { _ in (403, Data("missing or invalid token\n".utf8)) }
        #expect(await client().perform(.up, project: "p") == .tokenChanged)
        FakeURLProtocol.reset { _ in (403, Data("origin not allowed\n".utf8)) }
        #expect(await client().perform(.up, project: "p") == .refused("origin not allowed"))
        FakeURLProtocol.reset { _ in (200, Data(#"{"ok":false,"message":"compose failed"}"#.utf8)) }
        #expect(await client().perform(.up, project: "p") == .failed("compose failed"))
        FakeURLProtocol.reset { _ in (500, Data("boom\n".utf8)) }
        #expect(await client().perform(.up, project: "p") == .failed("boom"))
    }

    @Test func theActionTimeoutIsNotBelowFifteenMinutes() throws {
        let source = try String(contentsOf: Sources.file("APIClient.swift"), encoding: .utf8)
        #expect(source.contains("16 * 60"))
    }

    @Test func sourcesNameNoOtherApiPath() throws {
        var found = Set<String>()
        for url in try Sources.all() {
            let text = try String(contentsOf: url, encoding: .utf8)
            let regex = try NSRegularExpression(pattern: #"/api/[A-Za-z0-9_/-]*"#)
            for match in regex.matches(in: text, range: NSRange(text.startIndex..., in: text)) {
                found.insert(String(text[Range(match.range, in: text)!]))
            }
        }
        #expect(found == ["/api/instances", "/api/actions"])
    }

    @Test func noSourceStoresTheTokenOutsideMemory() throws {
        for url in try Sources.all() {
            let text = try String(contentsOf: url, encoding: .utf8)
            #expect(!text.contains("UserDefaults"), "\(url.lastPathComponent)")
            #expect(!text.contains("NSLog"), "\(url.lastPathComponent)")
        }
    }
}

enum Sources {
    static let dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appendingPathComponent("Sources/Stackfork")
    static func file(_ name: String) -> URL { dir.appendingPathComponent(name) }
    static func all() throws -> [URL] {
        try FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil).filter { $0.pathExtension == "swift" }
    }
}
