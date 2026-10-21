import Foundation

/// A cancellable delayed call.
@MainActor
final class ScheduledTask {
    private(set) var isCancelled = false
    private let onCancel: () -> Void
    init(onCancel: @escaping () -> Void = {}) { self.onCancel = onCancel }
    func cancel() {
        guard !isCancelled else { return }
        isCancelled = true
        onCancel()
    }
}

/// Time and delayed calls, injected so tests move a clock instead of waiting.
@MainActor
protocol Scheduler: AnyObject {
    func now() -> TimeInterval
    func schedule(after delay: TimeInterval, _ work: @escaping @MainActor () -> Void) -> ScheduledTask
}

@MainActor
final class DispatchScheduler: Scheduler {
    func now() -> TimeInterval { ProcessInfo.processInfo.systemUptime }

    func schedule(after delay: TimeInterval, _ work: @escaping @MainActor () -> Void) -> ScheduledTask {
        let item = DispatchWorkItem { MainActor.assumeIsolated { work() } }
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: item)
        return ScheduledTask(onCancel: { item.cancel() })
    }
}

/// Runs `body` on the main actor: at once when already on the main thread,
/// else through a task. Child callbacks arrive on background threads.
func onMain(_ body: @escaping @MainActor @Sendable () -> Void) {
    if Thread.isMainThread {
        MainActor.assumeIsolated { body() }
    } else {
        Task { @MainActor in body() }
    }
}
