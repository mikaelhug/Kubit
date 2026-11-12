import Foundation

@MainActor
enum CommandLineTool {
    static let link = "/usr/local/bin/kubit"

    static var bundled: URL? {
        guard let url = Bundle.main.resourceURL?.appendingPathComponent("bin/kubit"),
              FileManager.default.isExecutableFile(atPath: url.path) else { return nil }
        return url
    }

    static var linked: Bool {
        guard let bundled,
              let dest = try? FileManager.default.destinationOfSymbolicLink(atPath: link) else { return false }
        let target = URL(fileURLWithPath: dest, relativeTo: URL(fileURLWithPath: "/usr/local/bin", isDirectory: true))
        return target.standardizedFileURL.resolvingSymlinksInPath().path
            == bundled.standardizedFileURL.resolvingSymlinksInPath().path
    }

    private static var occupied: Bool {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: link) else { return false }
        return attrs[.type] as? FileAttributeType != .typeSymbolicLink
    }

    static func install() async {
        guard let bundled else { return }
        if occupied {
            Updater.alert("\(link) is not a link.")
            return
        }
        let script = """
        on run argv
            do shell script "test ! -e /usr/local/bin/kubit -o -L /usr/local/bin/kubit || exit 3; mkdir -p /usr/local/bin && ln -sfh " & quoted form of (item 1 of argv) & " /usr/local/bin/kubit" with administrator privileges
        end run
        """
        let (status, stderr) = await Task.detached { () -> (Int32, String) in
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
            p.arguments = ["-e", script, bundled.path]
            let err = Pipe()
            p.standardError = err
            p.standardOutput = FileHandle.nullDevice
            do { try p.run() } catch { return (-1, error.localizedDescription) }
            let data = err.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            return (p.terminationStatus, String(decoding: data, as: UTF8.self))
        }.value
        guard status != 0 else { return }
        if stderr.contains("(-128)") { return }
        Updater.alert("The command line tool could not be installed.", detail: stderr.trimmingCharacters(in: .whitespacesAndNewlines))
    }
}
