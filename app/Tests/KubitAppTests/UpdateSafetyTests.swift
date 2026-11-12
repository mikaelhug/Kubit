import Foundation
import Testing
@testable import KubitApp

@MainActor
struct UpdateSafetyTests {
    private let fixtures = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Fixtures")

    private func temp() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    @Test("an update with a bad or missing signature is refused; the fixture is signed by Go crypto/ed25519 as the release tool does")
    func signature() throws {
        let zip = fixtures.appendingPathComponent("release.zip")
        let sig = fixtures.appendingPathComponent("release.zip.sig")
        let key = try String(contentsOf: fixtures.appendingPathComponent("release.pub"), encoding: .utf8)
        #expect(Updater.verify(zip: zip, sig: sig, key: key))

        let dir = try temp()
        defer { try? FileManager.default.removeItem(at: dir) }
        var bytes = try Data(contentsOf: zip)
        bytes[bytes.count / 2] ^= 0x01
        let tampered = dir.appendingPathComponent("tampered.zip")
        try bytes.write(to: tampered)
        #expect(!Updater.verify(zip: tampered, sig: sig, key: key))

        var signature = Data(base64Encoded: try String(contentsOf: sig, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines))!
        signature[0] ^= 0x01
        let badSig = dir.appendingPathComponent("bad.sig")
        try signature.base64EncodedString().write(to: badSig, atomically: true, encoding: .utf8)
        #expect(!Updater.verify(zip: zip, sig: badSig, key: key))

        #expect(!Updater.verify(zip: zip, sig: dir.appendingPathComponent("missing.sig"), key: key))
        #expect(!Updater.verify(zip: zip, sig: sig, key: nil))
        #expect(!Updater.verify(zip: zip, sig: sig, key: ""))
    }

    @Test("a staged bundle that is not newer is refused")
    func stagedVersion() throws {
        let dir = try temp()
        defer { try? FileManager.default.removeItem(at: dir) }
        func app(_ version: String?) throws -> URL {
            let app = dir.appendingPathComponent("\(version ?? "none").app", isDirectory: true)
            let contents = app.appendingPathComponent("Contents", isDirectory: true)
            try FileManager.default.createDirectory(at: contents, withIntermediateDirectories: true)
            if let version {
                try (["CFBundleShortVersionString": version] as NSDictionary)
                    .write(to: contents.appendingPathComponent("Info.plist"))
            }
            return app
        }
        #expect(Updater.stagedIsNewer(try app("0.10.0"), than: "0.9.1"))
        #expect(!Updater.stagedIsNewer(try app("0.2.0"), than: "0.2.0"))
        #expect(!Updater.stagedIsNewer(try app("0.1.9"), than: "0.2.0"))
        #expect(!Updater.stagedIsNewer(try app("0.3.0-rc1"), than: "0.2.0"))
        #expect(!Updater.stagedIsNewer(try app(nil), than: "0.2.0"))
    }
}
