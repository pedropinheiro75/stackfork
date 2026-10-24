import Foundation
import Testing

/// Every suite that uses FakeURLProtocol nests here: its recorder is shared, so
/// they must not run at the same time as each other.
@Suite(.serialized) enum FakeNetworkTests {}

struct RecordedRequest: @unchecked Sendable {
    let method: String
    let path: String
    let headers: [String: String]
    let body: Data
}

/// Answers requests from a closure and records them. One handler at a time:
/// suites that use it run serialized.
final class FakeURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((RecordedRequest) -> (Int, Data))?
    nonisolated(unsafe) static var requests: [RecordedRequest] = []
    private static let lock = NSLock()

    static func reset(_ handler: @escaping (RecordedRequest) -> (Int, Data)) {
        lock.lock(); defer { lock.unlock() }
        self.handler = handler
        requests = []
    }

    static var recorded: [RecordedRequest] { lock.lock(); defer { lock.unlock() }; return requests }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FakeURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buffer, maxLength: buffer.count)
                if n <= 0 { break }
                body.append(buffer, count: n)
            }
            stream.close()
        }
        let recorded = RecordedRequest(method: request.httpMethod ?? "", path: request.url?.path ?? "",
                                       headers: request.allHTTPHeaderFields ?? [:], body: body)
        Self.lock.lock()
        Self.requests.append(recorded)
        let handler = Self.handler
        Self.lock.unlock()
        let (status, data) = handler?(recorded) ?? (500, Data())
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

let instancesJSON = """
{"docker":{"reachable":true},"instances":[
 {"project":"sf-acme-web-main-0123456789ab","root":"/w/acme-web","branch":"main","app":"acme-web","rel_root":"main checkout","is_main":true,"legacy":false,"state":"running","used":"2026-10-20T10:00:00Z"},
 {"project":"sf-acme-web-feature-login-0123456789ab","root":"/w/acme-web/.worktrees/feature-login","branch":"feature-login","app":"acme-web","rel_root":".worktrees/feature-login","legacy":false,"state":"partial","used":"2026-10-20T09:00:00Z"},
 {"project":"sf-acme-web-feature-billing-0123456789ab","root":"/w/acme-web/.worktrees/feature-billing","branch":"feature-billing","app":"acme-web","legacy":false,"state":"stopped","used":"2026-10-19T09:00:00Z"},
 {"project":"acme-api-old-feature-0123456789ab","branch":"old-feature","legacy":true,"state":"waiting"},
 {"project":"sf-acme-api-main-aaaaaaaaaaaa","branch":"main","app":"acme-api","legacy":false,"state":"unknown","used":"2026-10-21T09:00:00.123Z"}
]}
"""
