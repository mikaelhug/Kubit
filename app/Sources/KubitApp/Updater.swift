import AppKit
import CryptoKit
import Foundation
import Observation
import UserNotifications

struct Release: Equatable, Sendable {
    let tag: String
    let version: String
    let zip: URL
    let zipName: String
    let sig: URL
    let sigName: String
}

struct UpdateError: Error {
    let message: String
    var archive: URL?
}

@MainActor
@Observable
final class Updater {
    private(set) var available: Release?
    private(set) var checking = false
    private(set) var installing = false
    var automatic: Bool = UserDefaults.standard.object(forKey: Keys.automatic) as? Bool ?? true {
        didSet { UserDefaults.standard.set(automatic, forKey: Keys.automatic) }
    }

    @ObservationIgnored private let daemon: Daemon
    @ObservationIgnored var terminating = false

    private enum Keys {
        static let automatic = "checkAutomatically"
        static let lastCheck = "lastUpdateCheck"
        static let notified = "notifiedVersion"
    }

    nonisolated static let category = "update"
    nonisolated static let installAction = "install"

    init(daemon: Daemon) {
        self.daemon = daemon
    }

    var current: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0"
    }

    func checkOnLaunch() async {
        guard automatic else { return }
        await check(manual: false)
    }

    func checkAfterWake() async {
        guard automatic else { return }
        let last = UserDefaults.standard.object(forKey: Keys.lastCheck) as? Date ?? .distantPast
        guard Date().timeIntervalSince(last) > 24 * 3600 else { return }
        await check(manual: false)
    }

    func check(manual: Bool) async {
        guard !checking else { return }
        checking = true
        defer { checking = false }
        UserDefaults.standard.set(Date(), forKey: Keys.lastCheck)
        guard let feed = (Bundle.main.object(forInfoDictionaryKey: "KubitUpdateFeed") as? String)
            .flatMap(URL.init(string:)), feed.scheme == "https" else {
            if manual { Self.alert("Kubit has no update feed.") }
            return
        }
        switch await Self.latest(feed: feed, current: current) {
        case .newer(let release):
            available = release
            notify(release)
        case .current:
            available = nil
            if manual { Self.alert("Kubit is up to date.") }
        case .failed:
            if manual { Self.alert("Kubit could not check for updates.") }
        }
    }

    private enum Check {
        case newer(Release)
        case current
        case failed
    }

    private struct Feed: Decodable {
        struct Asset: Decodable {
            let name: String
            let browser_download_url: String
        }
        let tag_name: String
        let assets: [Asset]
    }

    private static func latest(feed: URL, current: String) async -> Check {
        var request = URLRequest(url: feed, timeoutInterval: 20)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("kubit-app", forHTTPHeaderField: "User-Agent")
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200,
              let json = try? JSONDecoder().decode(Feed.self, from: data) else { return .failed }
        guard let latest = version(tag: json.tag_name), let mine = version(json: current),
              latest.lexicographicallyPrecedes(mine) == false, latest != mine else { return .current }
        let v = latest.map(String.init).joined(separator: ".")
        let zipName = "Kubit-\(v)-macos-arm64.zip"
        let sigName = zipName + ".sig"
        guard let zip = json.assets.first(where: { $0.name == zipName }).flatMap({ URL(string: $0.browser_download_url) }),
              let sig = json.assets.first(where: { $0.name == sigName }).flatMap({ URL(string: $0.browser_download_url) }),
              zip.scheme == "https", sig.scheme == "https" else { return .current }
        return .newer(Release(tag: json.tag_name, version: v, zip: zip, zipName: zipName, sig: sig, sigName: sigName))
    }

    static func version(tag: String) -> [Int]? {
        guard let m = tag.wholeMatch(of: /v(\d{1,9})\.(\d{1,9})\.(\d{1,9})/) else { return nil }
        return [Int(m.1)!, Int(m.2)!, Int(m.3)!]
    }

    static func version(json: String) -> [Int]? {
        version(tag: "v" + json)
    }

    static func newer(_ staged: String, than current: String) -> Bool {
        guard let a = version(json: staged), let b = version(json: current) else { return false }
        return b.lexicographicallyPrecedes(a)
    }

    private func notify(_ release: Release) {
        let defaults = UserDefaults.standard
        guard Bundle.main.bundleIdentifier != nil,
              defaults.string(forKey: Keys.notified) != release.version else { return }
        defaults.set(release.version, forKey: Keys.notified)
        let center = UNUserNotificationCenter.current()
        let content = UNMutableNotificationContent()
        content.title = "Kubit \(release.version) is available."
        content.categoryIdentifier = Self.category
        content.userInfo = ["tag": release.tag]
        let request = UNNotificationRequest(identifier: "update-\(release.tag)", content: content, trigger: nil)
        Task {
            guard (try? await center.requestAuthorization(options: [.alert])) == true else { return }
            try? await center.add(request)
        }
    }

    func install() async {
        guard let release = available, !installing else { return }
        installing = true
        defer { installing = false }
        do {
            try await install(release)
        } catch let error as UpdateError {
            Self.alert("Kubit \(release.version) could not be installed.", detail: error.message)
            if let archive = error.archive { NSWorkspace.shared.activateFileViewerSelecting([archive]) }
        } catch {
            Self.alert("Kubit \(release.version) could not be installed.", detail: error.localizedDescription)
        }
    }

    private func install(_ release: Release) async throws {
        let fm = FileManager.default
        let target = Bundle.main.bundleURL
        guard target.pathExtension == "app" else { throw UpdateError(message: "Kubit is not running from an app bundle.") }
        guard [release.tag, release.zipName, release.sigName].allSatisfy(Self.safeComponent) else {
            throw UpdateError(message: "The release names are not safe file names.")
        }
        let dir = fm.homeDirectoryForCurrentUser
            .appendingPathComponent(".kubit/updates", isDirectory: true)
            .appendingPathComponent(release.tag, isDirectory: true)
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let zip = dir.appendingPathComponent(release.zipName)
        let sig = dir.appendingPathComponent(release.sigName)
        try await Self.download(release.zip, to: zip)
        do {
            try await Self.download(release.sig, to: sig)
        } catch let error as UpdateError {
            throw UpdateError(message: error.message, archive: zip)
        }
        guard Self.verify(zip: zip, sig: sig, key: Bundle.main.object(forInfoDictionaryKey: "KubitReleaseKey") as? String) else {
            throw UpdateError(message: "The signature does not match.", archive: zip)
        }

        let parent = target.deletingLastPathComponent()
        guard fm.isWritableFile(atPath: parent.path) else {
            throw UpdateError(message: "\(parent.path) is not writable.", archive: zip)
        }
        let staging = parent.appendingPathComponent(".kubit-update-\(release.tag)", isDirectory: true)
        try? fm.removeItem(at: staging)
        guard await Self.run("/usr/bin/ditto", ["-x", "-k", zip.path, staging.path]) == 0,
              let staged = (try? fm.contentsOfDirectory(at: staging, includingPropertiesForKeys: nil))?
                .first(where: { $0.pathExtension == "app" }) else {
            try? fm.removeItem(at: staging)
            throw UpdateError(message: "The archive could not be unpacked.", archive: zip)
        }
        guard Self.stagedIsNewer(staged, than: current) else {
            try? fm.removeItem(at: staging)
            throw UpdateError(message: "The downloaded app is not newer than \(current).", archive: zip)
        }

        guard await daemon.confirmStop(owned: true) else {
            try? fm.removeItem(at: staging)
            return
        }
        let wasRunning = daemon.ownsChild
        if wasRunning { await daemon.stop() }
        do {
            _ = try fm.replaceItemAt(target, withItemAt: staged)
        } catch {
            try? fm.removeItem(at: staging)
            if wasRunning { await daemon.start() }
            throw UpdateError(message: error.localizedDescription, archive: zip)
        }
        try? fm.removeItem(at: staging)
        _ = await Self.run("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", ["-f", target.path])
        guard await Self.run("/usr/bin/open", ["-n", target.path]) == 0 else {
            throw UpdateError(message: "The new app could not be opened.", archive: target)
        }
        terminating = true
        NSApp.terminate(nil)
    }

    static func safeComponent(_ s: String) -> Bool {
        !s.isEmpty && s.count <= 128 && !s.hasPrefix(".")
            && s.unicodeScalars.allSatisfy { $0.isASCII && (CharacterSet.alphanumerics.contains($0) || "._-+".unicodeScalars.contains($0)) }
    }

    static func stagedIsNewer(_ app: URL, than current: String) -> Bool {
        let version = NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist"))?["CFBundleShortVersionString"] as? String ?? ""
        return newer(version, than: current)
    }

    static func verify(zip: URL, sig: URL, key keyText: String?) -> Bool {
        guard let keyText,
              let raw = Data(base64Encoded: keyText.trimmingCharacters(in: .whitespacesAndNewlines)), raw.count == 32,
              let key = try? Curve25519.Signing.PublicKey(rawRepresentation: raw),
              let sigText = try? String(contentsOf: sig, encoding: .utf8),
              let signature = Data(base64Encoded: sigText.trimmingCharacters(in: .whitespacesAndNewlines)), signature.count == 64,
              let data = try? Data(contentsOf: zip, options: .mappedIfSafe) else { return false }
        return key.isValidSignature(signature, for: data)
    }

    private static func download(_ url: URL, to dest: URL) async throws {
        var request = URLRequest(url: url, timeoutInterval: 60)
        request.setValue("kubit-app", forHTTPHeaderField: "User-Agent")
        guard let (tmp, response) = try? await URLSession.shared.download(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw UpdateError(message: "\(dest.lastPathComponent) could not be downloaded.")
        }
        let fm = FileManager.default
        try? fm.removeItem(at: dest)
        do {
            try fm.moveItem(at: tmp, to: dest)
        } catch {
            throw UpdateError(message: error.localizedDescription)
        }
    }

    static func run(_ path: String, _ args: [String]) async -> Int32 {
        await Task.detached {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: path)
            p.arguments = args
            do {
                try p.run()
                p.waitUntilExit()
                return p.terminationStatus
            } catch {
                return -1
            }
        }.value
    }

    static func alert(_ message: String, detail: String? = nil) {
        let alert = NSAlert()
        alert.messageText = message
        if let detail { alert.informativeText = detail }
        NSApp.activate()
        alert.runModal()
    }
}

final class NotificationHandler: NSObject, UNUserNotificationCenterDelegate {
    let onInstall: @MainActor @Sendable () -> Void

    init(onInstall: @escaping @MainActor @Sendable () -> Void) {
        self.onInstall = onInstall
    }

    func register() {
        guard Bundle.main.bundleIdentifier != nil else { return }
        let center = UNUserNotificationCenter.current()
        let install = UNNotificationAction(identifier: Updater.installAction, title: "Install", options: [])
        center.setNotificationCategories([UNNotificationCategory(identifier: Updater.category, actions: [install], intentIdentifiers: [])])
        center.delegate = self
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        guard response.actionIdentifier == Updater.installAction else { return }
        await onInstall()
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner]
    }
}
