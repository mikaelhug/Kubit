import AppKit
import Observation
import SwiftUI

@MainActor
final class AppModel {
    static let shared = AppModel()
    let daemon = Daemon()
    let updater: Updater
    let shell = ShellState()
    lazy var notifications = NotificationHandler { [weak self] in
        Task { await self?.updater.install() }
    }

    private init() {
        updater = Updater(daemon: daemon)
    }
}

@MainActor
@Observable
final class ShellState {
    var loginItem = LoginItem.enabled
    var cliLinked = CommandLineTool.bundled == nil || CommandLineTool.linked

    func refresh() {
        loginItem = LoginItem.enabled
        cliLinked = CommandLineTool.bundled == nil || CommandLineTool.linked
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var wake: NSObjectProtocol?

    func applicationDidFinishLaunching(_ notification: Notification) {
        let model = AppModel.shared
        model.notifications.register()
        wake = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
        ) { _ in
            Task { @MainActor in await AppModel.shared.updater.checkAfterWake() }
        }
        Task {
            await model.daemon.start()
            await model.updater.checkOnLaunch()
        }
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        let model = AppModel.shared
        if model.updater.terminating || !model.daemon.ownsChild { return .terminateNow }
        if DaemonAPI.applyRunning() && !Daemon.confirmApplyRunning() { return .terminateCancel }
        guard let child = model.daemon.prepareForTerminate() else { return .terminateNow }
        DispatchQueue.global().async {
            child.stop()
            // AppKit waits out .terminateLater in a nested run loop where the main dispatch queue is not drained.
            RunLoop.main.perform(inModes: [.common, .default, .modalPanel]) {
                MainActor.assumeIsolated { NSApp.reply(toApplicationShouldTerminate: true) }
            }
        }
        return .terminateLater
    }
}

@main
struct KubitApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    private let model = AppModel.shared

    var body: some Scene {
        let badged = model.updater.available != nil
        MenuBarExtra {
            MenuContent(daemon: model.daemon, updater: model.updater, shell: model.shell)
        } label: {
            Image(nsImage: MenuIcon.image(badged: badged))
        }
        .menuBarExtraStyle(.menu)
    }
}

struct MenuContent: View {
    let daemon: Daemon
    let updater: Updater
    let shell: ShellState

    var body: some View {
        Text(daemon.statusLine)
        Divider()
        Button("Open Console") { daemon.openConsole() }
            .disabled(!daemon.answers)
        switch daemon.state {
        case .stopped, .failed:
            Button("Start Kubit") { Task { await daemon.start() } }
        default:
            Button("Stop Kubit") {
                Task {
                    guard await daemon.confirmStop(owned: false) else { return }
                    await daemon.stop()
                }
            }
            .disabled(daemon.state == .stopping)
        }
        Divider()
        Menu("Workspace") {
            if let workspace = daemon.workspace {
                Button((workspace.path as NSString).abbreviatingWithTildeInPath) {
                    NSWorkspace.shared.activateFileViewerSelecting([workspace])
                }
                Divider()
            }
            Button("Choose Workspace") { Task { await daemon.chooseWorkspace() } }
        }
        Divider()
        if let release = updater.available {
            Button("Install Kubit \(release.version)") { Task { await updater.install() } }
                .disabled(updater.installing)
        }
        Button("Check for Updates") { Task { await updater.check(manual: true) } }
            .disabled(updater.checking || updater.installing)
        Toggle("Check Automatically", isOn: Binding(
            get: { updater.automatic },
            set: { updater.automatic = $0 }
        ))
        Toggle("Launch at Login", isOn: Binding(
            get: { shell.loginItem },
            set: {
                LoginItem.set($0)
                shell.refresh()
            }
        ))
        if !shell.cliLinked {
            Button("Install Command Line Tool") {
                Task {
                    await CommandLineTool.install()
                    shell.refresh()
                }
            }
        }
        Divider()
        Button("Quit Kubit") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

enum MenuIcon {
    @MainActor private static let base: NSImage = {
        let image = Bundle.main.url(forResource: "MenuBarIcon", withExtension: "svg")
            .flatMap(NSImage.init(contentsOf:))
            ?? NSImage(systemSymbolName: "cube", accessibilityDescription: "Kubit")
            ?? NSImage(size: NSSize(width: 18, height: 18))
        image.size = NSSize(width: 18, height: 18)
        image.isTemplate = true
        return image
    }()

    @MainActor private static let badged: NSImage = {
        let icon = base
        let image = NSImage(size: icon.size, flipped: false) { rect in
            icon.draw(in: rect)
            let dot = NSRect(x: rect.maxX - 7, y: rect.maxY - 7, width: 7, height: 7)
            NSGraphicsContext.current?.compositingOperation = .clear
            NSBezierPath(ovalIn: dot.insetBy(dx: -1.5, dy: -1.5)).fill()
            NSGraphicsContext.current?.compositingOperation = .sourceOver
            NSColor.black.setFill()
            NSBezierPath(ovalIn: dot).fill()
            return true
        }
        image.isTemplate = true
        return image
    }()

    @MainActor static func image(badged on: Bool) -> NSImage { on ? badged : base }
}
