import AppKit
import Foundation
import Observation

enum DaemonState: Equatable {
    case starting
    case running(String)
    case external(String)
    case stopping
    case stopped
    case failed(String)
}

struct Hello: Decodable, Sendable {
    let version: String
}

enum DaemonAPI {
    static let base = URL(string: "http://127.0.0.1:8090")!
    static let live = URL(string: "ws://127.0.0.1:8090/api/v1/ws")!

    static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.connectionProxyDictionary = [:]
        config.timeoutIntervalForRequest = 5
        return URLSession(configuration: config)
    }()

    private struct LiveMessage: Decodable {
        let kind: String
        let hello: Hello?
    }

    static func hello(in message: URLSessionWebSocketTask.Message) -> Hello? {
        let data: Data
        switch message {
        case .string(let s): data = Data(s.utf8)
        case .data(let d): data = d
        @unknown default: return nil
        }
        guard let m = try? JSONDecoder().decode(LiveMessage.self, from: data), m.kind == "hello" else { return nil }
        return m.hello
    }

    static func probe(timeout: Duration = .seconds(2)) async -> Hello? {
        let task = session.webSocketTask(with: live)
        task.resume()
        let hello = await withTaskGroup(of: Hello?.self) { group in
            group.addTask { (try? await task.receive()).flatMap(hello(in:)) }
            group.addTask {
                try? await Task.sleep(for: timeout)
                task.cancel(with: .goingAway, reason: nil)
                return nil
            }
            let first = await group.next() ?? nil
            task.cancel(with: .goingAway, reason: nil)
            group.cancelAll()
            return first
        }
        return hello
    }

    private final class Box: @unchecked Sendable {
        var status = 0
        var data = Data()
    }

    static func call(_ method: String, _ components: [String], timeout: TimeInterval = 3) -> (status: Int, data: Data)? {
        var url = base
        for c in components { url.append(component: c) }
        var request = URLRequest(url: url, timeoutInterval: timeout)
        request.httpMethod = method
        let box = Box()
        let done = DispatchSemaphore(value: 0)
        session.dataTask(with: request) { data, response, _ in
            box.status = (response as? HTTPURLResponse)?.statusCode ?? 0
            box.data = data ?? Data()
            done.signal()
        }.resume()
        guard done.wait(timeout: .now() + timeout + 1) == .success, box.status != 0 else { return nil }
        return (box.status, box.data)
    }

    static func requestStop() -> Bool {
        call("POST", ["api", "v1", "daemon", "stop"])?.status == 202
    }

    private struct ClusterRow: Decodable { let name: String }
    private struct ApplyRun: Decodable { let running: Bool }

    static func applyRunning() -> Bool {
        guard let list = call("GET", ["api", "v1", "clusters"]), list.status == 200,
              let rows = try? JSONDecoder().decode([ClusterRow].self, from: list.data) else { return false }
        return rows.contains { row in
            guard let r = call("GET", ["api", "v1", "clusters", row.name, "apply"]), r.status == 200,
                  let run = try? JSONDecoder().decode(ApplyRun.self, from: r.data) else { return false }
            return run.running
        }
    }
}

final class LogSink: @unchecked Sendable {
    private let lock = NSLock()
    private let file: FileHandle?
    private var partial = Data()
    private var lines: [String] = []
    private var ready = false
    private let onReady: @Sendable () -> Void

    init(file: FileHandle?, onReady: @escaping @Sendable () -> Void) {
        self.file = file
        self.onReady = onReady
    }

    func write(_ data: Data) {
        guard !data.isEmpty else { return }
        let fire: Bool = lock.withLock {
            try? file?.write(contentsOf: data)
            partial.append(data)
            var fire = false
            while let nl = partial.firstIndex(of: 0x0A) {
                let line = String(decoding: partial[partial.startIndex..<nl], as: UTF8.self)
                partial.removeSubrange(partial.startIndex...nl)
                lines.append(line)
                if lines.count > 40 { lines.removeFirst() }
                if !ready && line.contains(" listening on ") {
                    ready = true
                    fire = true
                }
            }
            return fire
        }
        if fire { onReady() }
    }

    func drain(_ fd: Int32) {
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
        var buf = [UInt8](repeating: 0, count: 65536)
        while true {
            let n = read(fd, &buf, buf.count)
            if n <= 0 { break }
            write(Data(buf[0..<n]))
        }
        lock.withLock { try? file?.close() }
    }

    var bindFailed: Bool {
        lock.withLock { lines.contains { $0.contains("address already in use") } }
    }

    var lastError: String? {
        lock.withLock {
            guard let line = lines.last(where: { $0.hasPrefix("error: ") }) else { return nil }
            let msg = line.dropFirst("error: ".count)
            guard let first = msg.first else { return nil }
            let text = first.uppercased() + msg.dropFirst()
            return text.hasSuffix(".") ? text : text + "."
        }
    }
}

final class Child: @unchecked Sendable {
    let process: Process
    let sink: LogSink
    let started = Date()

    init(process: Process, sink: LogSink) {
        self.process = process
        self.sink = sink
    }

    var running: Bool { process.isRunning }

    private func waitExit(_ seconds: Double) -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while process.isRunning && Date() < deadline { usleep(100_000) }
        return !process.isRunning
    }

    func stop() {
        guard process.isRunning else { return }
        let pid = process.processIdentifier
        if !DaemonAPI.requestStop() { kill(pid, SIGTERM) }
        if waitExit(10) { return }
        kill(pid, SIGTERM)
        if waitExit(10) { return }
        kill(pid, SIGKILL)
        _ = waitExit(5)
    }
}

@MainActor
@Observable
final class Daemon {
    private(set) var state: DaemonState = .stopped
    private(set) var workspace: URL? = UserDefaults.standard.url(forKey: Keys.workspace)

    @ObservationIgnored private(set) var child: Child?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var failures = 0
    @ObservationIgnored private var intentional = false
    @ObservationIgnored private var respawn: Task<Void, Never>?
    @ObservationIgnored private var live: URLSessionWebSocketTask?
    @ObservationIgnored private var liveRetry = 0
    @ObservationIgnored private var rotated = false

    private enum Keys {
        static let workspace = "workspace"
        static let consoleOpened = "consoleOpened"
    }

    static var logFile: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/Kubit/kubit.log")
    }

    var appVersion: String? {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
    }

    var answers: Bool {
        switch state {
        case .running, .external: true
        default: false
        }
    }

    var ownsChild: Bool { child?.running == true }

    var statusLine: String {
        switch state {
        case .starting: "Kubit is starting."
        case .running(let v): "Kubit \(v)"
        case .external: "Running in a terminal."
        case .stopping: "Kubit is stopping."
        case .stopped: "Kubit is stopped."
        case .failed(let msg): msg
        }
    }

    func start() async {
        switch state {
        case .stopped, .failed: break
        default: return
        }
        state = .starting
        if let hello = await DaemonAPI.probe() {
            attachExternal(hello)
            return
        }
        guard let workspace = workspace ?? pickWorkspace() else {
            state = .stopped
            return
        }
        setWorkspace(workspace)
        failures = 0
        spawn()
    }

    func stop() async {
        respawn?.cancel()
        respawn = nil
        switch state {
        case .external(let version):
            state = .stopping
            if !(await Task.detached { DaemonAPI.requestStop() }.value), state == .stopping {
                state = .external(version)
            }
        default:
            if let child, child.running {
                intentional = true
                state = .stopping
                await Task.detached { child.stop() }.value
            }
            closeLive()
            state = .stopped
        }
    }

    func confirmStop(owned: Bool) async -> Bool {
        if owned ? !ownsChild : !answers { return true }
        guard await Task.detached(operation: { DaemonAPI.applyRunning() }).value else { return true }
        return Self.confirmApplyRunning()
    }

    static func confirmApplyRunning() -> Bool {
        let alert = NSAlert()
        alert.messageText = "An apply is running."
        alert.alertStyle = .warning
        let stop = alert.addButton(withTitle: "Stop anyway")
        stop.hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        NSApp.activate()
        return alert.runModal() == .alertFirstButtonReturn
    }

    func prepareForTerminate() -> Child? {
        respawn?.cancel()
        guard let child, child.running else { return nil }
        intentional = true
        state = .stopping
        return child
    }

    func chooseWorkspace() async {
        guard let url = pickWorkspace(), url != workspace else { return }
        guard ownsChild else {
            setWorkspace(url)
            return
        }
        guard await confirmStop(owned: true) else { return }
        await stop()
        setWorkspace(url)
        await start()
    }

    func openConsole() {
        NSWorkspace.shared.open(DaemonAPI.base)
    }

    private func setWorkspace(_ url: URL) {
        workspace = url
        UserDefaults.standard.set(url, forKey: Keys.workspace)
    }

    private func pickWorkspace() -> URL? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        panel.directoryURL = workspace
        NSApp.activate()
        return panel.runModal() == .OK ? panel.url : nil
    }

    private func openLog() -> FileHandle? {
        let fm = FileManager.default
        let log = Self.logFile
        try? fm.createDirectory(at: log.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !rotated {
            rotated = true
            let previous = log.appendingPathExtension("1")
            try? fm.removeItem(at: previous)
            try? fm.moveItem(at: log, to: previous)
        }
        if !fm.fileExists(atPath: log.path) { fm.createFile(atPath: log.path, contents: nil) }
        let handle = try? FileHandle(forWritingTo: log)
        _ = try? handle?.seekToEnd()
        return handle
    }

    private func spawn() {
        guard let exe = CommandLineTool.bundled, let workspace else {
            state = .failed("Kubit is missing from this app.")
            return
        }
        generation += 1
        let gen = generation
        intentional = false

        let process = Process()
        process.executableURL = exe
        process.arguments = ["serve", "--open=false", workspace.path]
        process.currentDirectoryURL = workspace
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:" + (env["PATH"] ?? "/usr/bin:/bin:/usr/sbin:/sbin")
        process.environment = env
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        process.standardInput = FileHandle.nullDevice

        let sink = LogSink(file: openLog()) { [weak self] in
            Task { @MainActor in self?.ready(gen) }
        }
        let reader = pipe.fileHandleForReading
        reader.readabilityHandler = { sink.write($0.availableData) }
        let fd = reader.fileDescriptor
        process.terminationHandler = { [weak self] p in
            reader.readabilityHandler = nil
            sink.drain(fd)
            let status = p.terminationStatus
            let clean = p.terminationReason == .exit && status == 0
            Task { @MainActor in await self?.exited(gen, status: status, clean: clean) }
        }
        do {
            try process.run()
        } catch {
            reader.readabilityHandler = nil
            state = .failed("Kubit could not start.")
            return
        }
        try? pipe.fileHandleForWriting.close()
        child = Child(process: process, sink: sink)
        state = .starting
    }

    private func ready(_ gen: Int) {
        guard gen == generation, ownsChild else { return }
        liveRetry = 0
        watch(gen)
    }

    private func exited(_ gen: Int, status: Int32, clean: Bool) async {
        guard gen == generation, let child else { return }
        self.child = nil
        closeLive()
        if intentional || clean {
            state = .stopped
            return
        }
        if child.sink.bindFailed {
            if let hello = await DaemonAPI.probe() {
                attachExternal(hello)
            } else {
                state = .failed("Port 8090 is in use.")
            }
            return
        }
        if Date().timeIntervalSince(child.started) >= 300 { failures = 0 }
        failures += 1
        let message = child.sink.lastError ?? "Kubit exited with status \(status)."
        guard failures < 5 else {
            state = .failed(message)
            return
        }
        state = .starting
        let delay = min(60, 1 << (failures - 1))
        respawn = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled, let self, self.state == .starting, self.child == nil else { return }
            self.spawn()
        }
    }

    private func attachExternal(_ hello: Hello) {
        generation += 1
        state = .external(Self.bare(hello.version))
        liveRetry = 0
        watch(generation)
        openConsoleOnFirstLaunch()
    }

    private func watch(_ gen: Int) {
        closeLive()
        let task = DaemonAPI.session.webSocketTask(with: DaemonAPI.live)
        task.maximumMessageSize = 16 << 20
        live = task
        task.resume()
        Task { [weak self] in
            var greeted = false
            while true {
                guard let message = try? await task.receive() else { break }
                if !greeted, let hello = DaemonAPI.hello(in: message) {
                    greeted = true
                    self?.connected(gen, hello)
                }
            }
            await self?.lost(gen, task)
        }
    }

    private func closeLive() {
        live?.cancel(with: .goingAway, reason: nil)
        live = nil
    }

    private func connected(_ gen: Int, _ hello: Hello) {
        guard gen == generation else { return }
        liveRetry = 0
        if ownsChild {
            state = .running(appVersion ?? Self.bare(hello.version))
            openConsoleOnFirstLaunch()
        }
    }

    private func lost(_ gen: Int, _ task: URLSessionWebSocketTask) async {
        guard gen == generation, live === task else { return }
        live = nil
        if ownsChild {
            guard !intentional else { return }
            liveRetry += 1
            try? await Task.sleep(for: .seconds(min(30, liveRetry)))
            if gen == generation, ownsChild, !intentional { watch(gen) }
            return
        }
        guard case .external = state else {
            if state == .stopping { state = .stopped }
            return
        }
        if let hello = await DaemonAPI.probe(), gen == generation {
            state = .external(Self.bare(hello.version))
            watch(gen)
        } else if gen == generation {
            state = .stopped
        }
    }

    private func openConsoleOnFirstLaunch() {
        let defaults = UserDefaults.standard
        guard !defaults.bool(forKey: Keys.consoleOpened) else { return }
        defaults.set(true, forKey: Keys.consoleOpened)
        openConsole()
    }

    private static func bare(_ version: String) -> String {
        version.hasPrefix("v") ? String(version.dropFirst()) : version
    }
}
