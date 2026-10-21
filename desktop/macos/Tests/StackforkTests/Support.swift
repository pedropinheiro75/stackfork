import Foundation
@testable import Stackfork

/// A clock the test moves. Work runs in time order when `advance` passes it.
@MainActor
final class ManualScheduler: Scheduler {
    private(set) var time: TimeInterval = 0
    private var pending: [(at: TimeInterval, seq: Int, task: ScheduledTask, work: @MainActor () -> Void)] = []
    private var seq = 0

    func now() -> TimeInterval { time }

    func schedule(after delay: TimeInterval, _ work: @escaping @MainActor () -> Void) -> ScheduledTask {
        seq += 1
        let task = ScheduledTask()
        pending.append((time + delay, seq, task, work))
        return task
    }

    func advance(by seconds: TimeInterval) {
        let target = time + seconds
        while let next = pending.filter({ !$0.task.isCancelled && $0.at <= target })
            .min(by: { ($0.at, $0.seq) < ($1.at, $1.seq) }) {
            pending.removeAll { $0.seq == next.seq }
            time = max(time, next.at)
            next.work()
        }
        time = target
        pending.removeAll { $0.task.isCancelled }
    }
}

let readyJSON = #"{"event":"ready","schema":1,"url":"http://127.0.0.1:41873/","port":41873,"token":"\#(String(repeating: "ab", count: 32))","pid":123,"version":"0.9.0"}"#

/// An in-memory child: the test decides what it prints and when it exits.
final class FakeChild: ChildProcess, @unchecked Sendable {
    let pid: Int32
    let events: ChildEvents
    private let lock = NSLock()
    private var _signals: [Int32] = []
    init(pid: Int32, events: ChildEvents) { self.pid = pid; self.events = events }
    var signals: [Int32] { lock.lock(); defer { lock.unlock() }; return _signals }
    func send(signal: Int32) { lock.lock(); _signals.append(signal); lock.unlock() }
    func closeStdin() {}
    func print(_ line: String) { events.stdout(line) }
    func exit(_ status: Int32 = 1) { events.exited(status) }
}

final class FakeLauncher: ChildLauncher, @unchecked Sendable {
    private let lock = NSLock()
    private var _children: [FakeChild] = []
    var children: [FakeChild] { lock.lock(); defer { lock.unlock() }; return _children }
    var last: FakeChild { children.last! }
    func launch(_ events: ChildEvents) throws -> any ChildProcess {
        lock.lock(); defer { lock.unlock() }
        let child = FakeChild(pid: Int32(1000 + _children.count), events: events)
        _children.append(child)
        return child
    }
}

/// Writes an executable shell script and returns its URL.
func makeScript(_ body: String, name: String = "child.sh") throws -> URL {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("sf-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let url = dir.appendingPathComponent(name)
    try ("#!/bin/sh\n" + body + "\n").write(to: url, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    return url
}

/// Polls `condition` on the main actor until it holds or `timeout` passes.
@MainActor
func eventually(timeout: TimeInterval = 10, _ condition: @MainActor () -> Bool) async -> Bool {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if condition() { return true }
        try? await Task.sleep(nanoseconds: 20_000_000)
    }
    return condition()
}
