import Foundation

/// The dashboard the app started: its URL and token, from the ready line only.
struct DashboardEndpoint: Equatable, Sendable {
    let baseURL: URL
    let token: String

    init?(_ ready: ReadyLine) {
        guard let url = URL(string: ready.url) else { return nil }
        baseURL = url
        token = ready.token
    }

    /// The dashboard's own origin, as the page sends it.
    var origin: String {
        "\(baseURL.scheme ?? "http")://\(baseURL.host ?? "127.0.0.1"):\(baseURL.port ?? 80)"
    }
}

struct InstancesResponse: Decodable, Equatable, Sendable {
    struct Docker: Decodable, Equatable, Sendable {
        let reachable: Bool
        let error: String?
    }

    struct Instance: Decodable, Equatable, Sendable {
        let project: String
        let root: String?
        let branch: String?
        let app: String?
        let relRoot: String?
        let isMain: Bool?
        let legacy: Bool?
        let state: String
        let used: String?

        enum CodingKeys: String, CodingKey {
            case project, root, branch, app, legacy, state, used
            case relRoot = "rel_root", isMain = "is_main"
        }
    }

    let docker: Docker
    let instances: [Instance]

    init(docker: Docker, instances: [Instance]) {
        self.docker = docker
        self.instances = instances
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        docker = try container.decode(Docker.self, forKey: .docker)
        instances = try container.decodeIfPresent([Instance].self, forKey: .instances) ?? []
    }

    enum CodingKeys: String, CodingKey { case docker, instances }
}

enum InstanceAction: String, Sendable {
    case up, down
}

enum ActionOutcome: Equatable, Sendable {
    case done(String)
    /// 409: the dashboard runs another action for this instance.
    case alreadyRunning
    /// `ok: false` or another error status, with the dashboard's message.
    case failed(String)
    /// 403: the token changed; wait for the next ready line instead of retrying.
    case tokenChanged
    case transport(String)
}

/// The app is a client of the dashboard like the page is (D8). It uses exactly
/// two endpoints and sends the headers the page sends.
final class APIClient: Sendable {
    let endpoint: DashboardEndpoint
    private let session: URLSession

    init(endpoint: DashboardEndpoint, session: URLSession? = nil) {
        self.endpoint = endpoint
        if let session {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.connectionProxyDictionary = [:] // loopback only, never a proxy
            self.session = URLSession(configuration: configuration)
        }
    }

    private func url(_ path: String) -> URL {
        URL(string: path, relativeTo: endpoint.baseURL)!.absoluteURL
    }

    func fetchInstances() async throws -> InstancesResponse {
        var request = URLRequest(url: url("/api/instances"))
        request.httpMethod = "GET"
        request.timeoutInterval = 10
        let (data, response) = try await session.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode(InstancesResponse.self, from: data)
    }

    func perform(_ action: InstanceAction, project: String) async -> ActionOutcome {
        struct Body: Encodable { let action: String; let project: String }
        var request = URLRequest(url: url("/api/actions"))
        request.httpMethod = "POST"
        // The dashboard waits up to 15 minutes for a running action to end.
        request.timeoutInterval = 16 * 60
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(endpoint.token, forHTTPHeaderField: "X-Stackfork-Token")
        request.setValue(endpoint.origin, forHTTPHeaderField: "Origin")
        request.httpBody = try? JSONEncoder().encode(Body(action: action.rawValue, project: project))
        do {
            let (data, response) = try await session.data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            switch status {
            case 409: return .alreadyRunning
            case 403: return .tokenChanged
            default: break
            }
            struct Reply: Decodable { let ok: Bool?; let message: String? }
            let reply = try? JSONDecoder().decode(Reply.self, from: data)
            if status == 200, reply?.ok == true { return .done(reply?.message ?? "") }
            let text = reply?.message ?? String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            return .failed(text.isEmpty ? "The dashboard answered \(status)." : text)
        } catch {
            return .transport(error.localizedDescription)
        }
    }
}
