import Foundation
import Testing
@testable import Stackfork

@MainActor
@Suite struct MenuModelTests {
    func inventory(_ json: String = instancesJSON) throws -> InstancesResponse {
        try JSONDecoder().decode(InstancesResponse.self, from: Data(json.utf8))
    }

    func model(_ state: Supervisor.State = .ready, inventory: InstancesResponse?, pending: [String: InstanceAction] = [:],
               defaultPath: Bool = false, now: TimeInterval = 0) -> MenuModel {
        MenuModel.make(state: state, now: now, usedDefaultPath: defaultPath, inventory: inventory, pending: pending)
    }

    @Test func groupsByAppWithStatesInWordsAndActionsPerState() throws {
        let m = model(inventory: try inventory())
        #expect(m.statusText == "Dashboard running")
        guard case .groups(let groups, let more) = m.list else { Issue.record("no groups"); return }
        #expect(more == 0)
        // acme-api has the newest activity but nothing live; acme-web has live instances: live first.
        #expect(groups.map(\.app) == ["acme-web", "acme-api", "acme-api-old-feature"])
        let web = groups[0]
        #expect(web.rows.map(\.branch) == ["main", "feature-login", "feature-billing"])
        #expect(web.rows.map(\.stateWord) == ["Running", "Partial", "Stopped"])
        #expect(web.rows.map(\.glyph) == ["●", "◐", "○"])
        #expect(web.rows[0].actions == [.stop, .openDashboard])
        #expect(web.rows[1].actions == [.start, .stop, .openDashboard])
        #expect(web.rows[2].actions == [.start, .openDashboard])
    }

    @Test func legacyWaitingAndUnknownRowsOnlyOpenTheDashboard() throws {
        let m = model(inventory: try inventory())
        guard case .groups(let groups, _) = m.list else { Issue.record("no groups"); return }
        let rows = groups.flatMap(\.rows)
        let legacy = try #require(rows.first { $0.project.hasPrefix("acme-api-old") })
        #expect(legacy.stateWord == "Waiting")
        #expect(legacy.actions == [.openDashboard])
        let unknown = try #require(rows.first { $0.stateWord == "Unknown" })
        #expect(unknown.actions == [.openDashboard])
        // the app name falls back to the project without prefix and hash
        #expect(groups.map(\.app).contains("acme-api-old-feature"))
    }

    @Test func pendingActionsShowStartingAndStoppingAndDisableActions() throws {
        let p = "sf-acme-web-main-0123456789ab"
        let m = model(inventory: try inventory(), pending: [p: .down, "sf-acme-web-feature-billing-0123456789ab": .up])
        guard case .groups(let groups, _) = m.list else { Issue.record("no groups"); return }
        let rows = groups.flatMap(\.rows)
        #expect(rows.first { $0.project == p }?.busy == "Stopping…")
        #expect(rows.first { $0.project == p }?.actionsEnabled == false)
        #expect(rows.first { $0.project.contains("billing") }?.busy == "Starting…")
        #expect(rows.first { $0.project.contains("billing") }?.title.hasSuffix("Starting…") == true)
    }

    @Test func dockerDownReplacesTheList() throws {
        let m = model(inventory: try inventory(#"{"docker":{"reachable":false,"error":"x"},"instances":[]}"#))
        #expect(m.list == .dockerDown)
        #expect(MenuModel.dockerDownText == "Docker is not running")
    }

    @Test func thirtyInstancesShowTwentyFiveRowsAndAMoreRow() throws {
        let items = (0..<30).map { i in
            #"{"project":"sf-bulk-b\#(i)-0123456789ab","branch":"b\#(i)","app":"bulk","legacy":false,"state":"stopped"}"#
        }.joined(separator: ",")
        let m = model(inventory: try inventory(#"{"docker":{"reachable":true},"instances":[\#(items)]}"#))
        guard case .groups(let groups, let more) = m.list else { Issue.record("no groups"); return }
        #expect(groups.flatMap(\.rows).count == 25)
        #expect(more == 5)
    }

    @Test func aChildThatIsNotReadyShowsNoListAndSaysWhy() throws {
        let inv = try inventory()
        #expect(model(.starting, inventory: inv).list == .none)
        #expect(model(.starting, inventory: inv).statusText == "Starting…")
        #expect(model(.restarting(at: 14), inventory: inv, now: 10).statusText == "Restarting in 4 s")
        let stopped = model(.stopped(reason: "port in use"), inventory: inv, defaultPath: true)
        #expect(stopped.statusText == "Stopped: port in use (using a default PATH)")
        #expect(stopped.showRestart && stopped.showLog)
        let restarting = model(.restarting(at: 2), inventory: nil, now: 0)
        #expect(!restarting.showRestart && restarting.showLog)
        #expect(!model(inventory: inv).showRestart && !model(inventory: inv).showLog)
    }

    @Test func readyWithoutAResponseYetShowsNoList() {
        #expect(model(inventory: nil).list == .none)
    }

    @Test func displayNames() {
        #expect(MenuModel.displayName("sf-acme-web-main-0123456789ab") == "acme-web-main")
        #expect(MenuModel.displayName("idv-thing") == "thing")
        #expect(MenuModel.displayName("plain") == "plain")
    }

    @Test func installLocationRule() {
        #expect(InstallLocation.allowsLoginItem(bundlePath: "/Applications/Stackfork.app", home: "/Users/x"))
        #expect(InstallLocation.allowsLoginItem(bundlePath: "/Users/x/Applications/Stackfork.app", home: "/Users/x"))
        #expect(!InstallLocation.allowsLoginItem(bundlePath: "/Volumes/Stackfork/Stackfork.app", home: "/Users/x"))
        #expect(!InstallLocation.allowsLoginItem(bundlePath: "/Users/x/Downloads/Stackfork.app", home: "/Users/x"))
        #expect(!InstallLocation.allowsLoginItem(bundlePath: "/private/var/folders/ab/T/AppTranslocation/1234-ABCD/d/Stackfork.app", home: "/Users/x"))
        #expect(!InstallLocation.allowsLoginItem(bundlePath: "/Applications/../tmp/Stackfork.app", home: "/Users/x"))
    }
}

@MainActor
@Suite(.serialized) struct PollerTests {
    @Test func noRequestWhileClosedAndThreeInElevenSecondsOpen() async throws {
        guard case .ready(let line) = parseReadyLine(readyJSON), let endpoint = DashboardEndpoint(line) else { return }
        FakeURLProtocol.reset { _ in (200, Data(instancesJSON.utf8)) }
        let api = APIClient(endpoint: endpoint, session: FakeURLProtocol.session())
        let clock = ManualScheduler()
        let poller = MenuPoller(scheduler: clock) { Task { _ = try? await api.fetchInstances() } }
        clock.advance(by: 60)
        try await Task.sleep(nanoseconds: 100_000_000)
        #expect(FakeURLProtocol.recorded.isEmpty)
        poller.menuOpened()
        clock.advance(by: 11)
        try await Task.sleep(nanoseconds: 300_000_000)
        #expect(FakeURLProtocol.recorded.count == 3)
        poller.menuClosed()
        clock.advance(by: 60)
        try await Task.sleep(nanoseconds: 100_000_000)
        #expect(FakeURLProtocol.recorded.count == 3)
    }
}
