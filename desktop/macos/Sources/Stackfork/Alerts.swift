import AppKit

struct AlertSpec: Equatable {
    var message: String
    var detail: String? = nil
    var buttons: [String]
}

/// Closes an alert that is still open.
@MainActor
final class AlertToken {
    private var onClose: (() -> Void)?
    init(onClose: @escaping () -> Void) { self.onClose = onClose }
    func close() {
        let close = onClose
        onClose = nil
        close?()
    }
}

/// Alerts are never modal: a nested modal loop entered from a main-queue job
/// stops the main queue, and with it the supervisor's callbacks and timers.
@MainActor
protocol AlertPresenting: AnyObject {
    /// Shows the alert and returns at once. `completion` gets the index of the
    /// button the person chose; it does not run when the token closes the alert.
    func present(_ spec: AlertSpec, completion: @escaping @MainActor (Int) -> Void) -> AlertToken
}

@MainActor
final class PanelAlertPresenter: AlertPresenting {
    @MainActor
    private final class ButtonTarget: NSObject {
        var chosen: (@MainActor (Int) -> Void)?
        @objc func clicked(_ sender: NSButton) { chosen?(sender.tag) }
    }

    private var open: [ObjectIdentifier: AlertToken] = [:]

    func present(_ spec: AlertSpec, completion: @escaping @MainActor (Int) -> Void) -> AlertToken {
        let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 420, height: 100),
                            styleMask: [.titled], backing: .buffered, defer: false)
        panel.title = "Stackfork"
        panel.isReleasedWhenClosed = false
        panel.level = .floating

        let message = NSTextField(wrappingLabelWithString: spec.message)
        message.font = .boldSystemFont(ofSize: 13)
        var views: [NSView] = [message]
        if let detail = spec.detail { views.append(NSTextField(wrappingLabelWithString: detail)) }
        let target = ButtonTarget()
        let buttons = NSStackView()
        buttons.orientation = .horizontal
        for (index, title) in spec.buttons.enumerated().reversed() {
            let button = NSButton(title: title, target: target, action: #selector(ButtonTarget.clicked(_:)))
            button.tag = index
            if index == 0 { button.keyEquivalent = "\r" }
            buttons.addArrangedSubview(button)
        }
        views.append(buttons)
        let stack = NSStackView(views: views)
        stack.orientation = .vertical
        stack.alignment = .trailing
        stack.spacing = 12
        stack.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 16, right: 20)
        for view in views.dropLast() { view.widthAnchor.constraint(equalToConstant: 380).isActive = true }
        panel.contentView = stack
        panel.setContentSize(stack.fittingSize)
        panel.center()

        var token: AlertToken!
        token = AlertToken { [weak self, weak panel] in
            panel?.orderOut(nil)
            _ = target // keep the button target alive until the alert closes
            self?.open[ObjectIdentifier(token)] = nil
        }
        open[ObjectIdentifier(token)] = token
        target.chosen = { index in
            token.close()
            completion(index)
        }
        NSApp.activate()
        panel.makeKeyAndOrderFront(nil)
        return token
    }
}

/// Quit (D7): SIGTERM through the supervisor, a **Quit Now** alert while the
/// child reports running actions, and the alert closes when the child exits.
@MainActor
final class QuitCoordinator {
    private let supervisor: Supervisor
    private let presenter: any AlertPresenting
    private let finished: () -> Void
    private var sheet: AlertToken?

    init(supervisor: Supervisor, presenter: any AlertPresenting, finished: @escaping () -> Void) {
        self.supervisor = supervisor
        self.presenter = presenter
        self.finished = finished
    }

    func begin() {
        supervisor.onWaiting = { [weak self] count in self?.showSheet(count) }
        supervisor.quit { [weak self] in
            self?.sheet?.close()
            self?.sheet = nil
            self?.finished()
        }
    }

    private func showSheet(_ count: Int) {
        guard sheet == nil else { return }
        sheet = presenter.present(AlertSpec(message: "Waiting for \(count) running action(s) to finish",
                                            detail: "Quit Now stops them.", buttons: ["Quit Now"])) { [weak self] _ in
            self?.sheet = nil
            self?.supervisor.quitNow()
        }
    }
}
