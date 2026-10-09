import Foundation
import Testing
@testable import ClaudeSwitchCore

/// F10: the update checker. Every test runs against the seams: a fake
/// network serving fixture bytes and an in-memory file system. Nothing here
/// touches the network or the disk outside a temporary directory.

/// The bytes each fixture asset serves; checksums.txt and
/// update-app-zip.sha256 hold their SHA-256.
enum ReleaseBytes {
    static let tarballArm = Data("fixture tarball darwin_arm64\n".utf8)
    static let tarballAmd = Data("fixture tarball darwin_amd64\n".utf8)
    static let appZip = Data("fixture app zip\n".utf8)
}

func fixtureRelease() throws -> Release {
    try #require(Release(data: try fixture("github-release-latest.json")))
}

@Suite struct UpdateVersionTests {
    @Test func testVersionsCompareNumerically() throws {
        func v(_ s: String) throws -> SemVer { try #require(SemVer(s), "\(s) parses") }
        #expect(try v("v0.5.6") > v("0.5.5"))
        #expect(try v("0.5.10") > v("0.5.9"))
        #expect(try v("0.6.0") > v("0.5.99"))
        #expect(try v("1.0.0") > v("0.99.99"))
        #expect(try v("0.5") == v("0.5.0"))
        #expect(try v("v0.5.6") == v("0.5.6"))
        #expect(try v("0.5.6-rc1") < v("0.5.6"), "a pre-release comes before its release")
        #expect(try v("0.5.6+build.7") == v("0.5.6"), "build metadata does not order")
        #expect(SemVer("dev") == nil)
        #expect(SemVer("") == nil)
        #expect(SemVer("0.x.1") == nil)
    }

    @Test func testANewerReleaseIsOffered() throws {
        let r = try fixtureRelease()
        #expect(UpdateOffer.available(current: "0.5.5", latest: r) == "0.5.6")
        #expect(UpdateOffer.available(current: "0.5.6", latest: r) == nil, "the same version")
        #expect(UpdateOffer.available(current: "0.6.0", latest: r) == nil, "a newer local build")
        #expect(UpdateOffer.available(current: nil, latest: r) == nil, "no bundle version: swift run")
        #expect(UpdateOffer.available(current: "dev", latest: r) == nil, "an unversioned build is not compared")
        #expect(UpdateOffer.footer("0.5.6") == "0.5.6 available")
    }

    @Test func testDraftsAndPreReleasesAreNotOffered() throws {
        var r = try fixtureRelease()
        r.prerelease = true
        #expect(UpdateOffer.available(current: "0.5.5", latest: r) == nil)
        r.prerelease = false
        r.draft = true
        #expect(UpdateOffer.available(current: "0.5.5", latest: r) == nil)
    }

    @Test func testTheCheckRunsOnceADayAndNeverWhenOff() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        #expect(UpdateSchedule.due(enabled: true, lastCheck: nil, now: now))
        #expect(!UpdateSchedule.due(enabled: true, lastCheck: now.addingTimeInterval(-23 * 3600), now: now))
        #expect(UpdateSchedule.due(enabled: true, lastCheck: now.addingTimeInterval(-24 * 3600), now: now))
        #expect(!UpdateSchedule.due(enabled: false, lastCheck: nil, now: now), "off means no call at all")
        #expect(UpdateSchedule.due(enabled: true, lastCheck: now.addingTimeInterval(3600), now: now),
                "a clock moved back does not stop the checks")
    }

    @Test func testTheReleaseURLComesFromOneRepository() {
        #expect(UpdateSource.latestURL(repo: "example/claudeswitch").absoluteString
                == "https://api.github.com/repos/example/claudeswitch/releases/latest")
        let parts = UpdateSource.repo.split(separator: "/")
        #expect(parts.count == 2 && parts[1] == "claudeswitch")
    }
}

@Suite struct UpdateAssetTests {
    @Test func testDecodesTheLatestRelease() throws {
        let r = try fixtureRelease()
        #expect(r.tag == "v0.5.6")
        #expect(r.version == SemVer("0.5.6"))
        #expect(r.assets.count == 6)
        #expect(!r.draft && !r.prerelease)
        #expect(Release(data: Data("[]".utf8)) == nil)
        #expect(Release(data: Data("{\"message\": \"Not Found\"}".utf8)) == nil, "the API's 404 body is no release")
    }

    @Test func testPicksTheAssetsForEachArchitecture() throws {
        let r = try fixtureRelease()
        let arm = try r.plan(arch: .arm64).get()
        #expect(arm.tarball.name == "claudeswitch_v0.5.6_darwin_arm64.tar.gz")
        #expect(arm.checksums.name == "checksums.txt")
        #expect(arm.appZip.name == "ClaudeSwitch-0.5.6-macos.zip")
        #expect(arm.appSHA.name == "ClaudeSwitch-0.5.6-macos.zip.sha256")
        let amd = try r.plan(arch: .amd64).get()
        #expect(amd.tarball.name == "claudeswitch_v0.5.6_darwin_amd64.tar.gz")
        #expect(amd.appZip == arm.appZip, "the app zip is universal")
    }

    @Test func testAMissingAssetIsSaidByName() throws {
        var r = try fixtureRelease()
        r.assets.removeAll { $0.name.hasSuffix("darwin_arm64.tar.gz") }
        #expect(r.plan(arch: .arm64).failure == .missingAsset("claudeswitch for darwin_arm64"))
        #expect(r.plan(arch: .amd64).failure == nil)
        r = try fixtureRelease()
        r.assets.removeAll { $0.name == "checksums.txt" }
        #expect(r.plan(arch: .arm64).failure == .missingAsset("checksums.txt"))
        r = try fixtureRelease()
        r.assets.removeAll { $0.name.hasSuffix(".sha256") }
        #expect(r.plan(arch: .arm64).failure == .missingAsset("ClaudeSwitch-0.5.6-macos.zip.sha256"))
    }

    @Test func testTheMacsArchitectureIsOneOfTheTwo() {
        #expect([UpdateArch.arm64, .amd64].contains(UpdateArch.current))
        #expect(UpdateArch.arm64.target == "darwin_arm64")
    }
}

@Suite struct ChecksumTests {
    @Test func testParsesChecksumsTxtAndShaFiles() throws {
        let sums = Checksums.parse(String(decoding: try fixture("checksums.txt"), as: UTF8.self))
        #expect(sums.count == 3)
        #expect(sums["claudeswitch_v0.5.6_darwin_arm64.tar.gz"] == "eaed22066005843938e7ab1e4ecdb7fc109d45f620470d3997e44ad6b8421343")
        let app = Checksums.parse(String(decoding: try fixture("update-app-zip.sha256"), as: UTF8.self))
        #expect(app["ClaudeSwitch-0.5.6-macos.zip"] == Checksums.sha256(ReleaseBytes.appZip))
        // shasum's binary marker, CRLF line ends, upper-case hex, and junk lines.
        let odd = Checksums.parse("ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789 *x.zip\r\nnot a line\n\n")
        #expect(odd == ["x.zip": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"])
    }

    @Test func testVerifiesBytesWithTheirSum() throws {
        let sums = Checksums.parse(String(decoding: try fixture("checksums.txt"), as: UTF8.self))
        let name = "claudeswitch_v0.5.6_darwin_arm64.tar.gz"
        #expect(Checksums.verify(ReleaseBytes.tarballArm, name: name, sums: sums).failure == nil)
        #expect(Checksums.verify(ReleaseBytes.tarballAmd, name: name, sums: sums).failure == .checksumMismatch(name))
        #expect(Checksums.verify(ReleaseBytes.tarballArm, name: "other.tar.gz", sums: sums).failure
                == .checksumMissing("other.tar.gz"))
    }

    @Test func testSha256IsLowerCaseHex() {
        #expect(Checksums.sha256(Data()) == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
    }
}

// MARK: the installer, through fakes

/// Serves fixture bytes by URL; records what was asked.
final class FakeNetwork: UpdateNetwork {
    var served: [String: Data] = [:]
    var asked: [String] = []
    var failing: Set<String> = []

    init(release: Data) throws {
        let base = "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v0.5.6/"
        served["https://api.github.com/repos/example/claudeswitch/releases/latest"] = release
        served[base + "checksums.txt"] = try fixture("checksums.txt")
        served[base + "ClaudeSwitch-0.5.6-macos.zip.sha256"] = try fixture("update-app-zip.sha256")
        served[base + "claudeswitch_v0.5.6_darwin_arm64.tar.gz"] = ReleaseBytes.tarballArm
        served[base + "claudeswitch_v0.5.6_darwin_amd64.tar.gz"] = ReleaseBytes.tarballAmd
        served[base + "ClaudeSwitch-0.5.6-macos.zip"] = ReleaseBytes.appZip
    }

    func data(from url: URL) throws -> Data {
        asked.append(url.absoluteString)
        if failing.contains(url.lastPathComponent) { throw UpdateError.download("\(url.lastPathComponent): offline") }
        guard let d = served[url.absoluteString] else { throw UpdateError.download("\(url.lastPathComponent): 404") }
        return d
    }
}

/// An in-memory file system: files by path, a directory being any prefix.
/// `tar` and `ditto` unpack to a fixed content; `install` and `cp` copy.
/// `failOn` makes the first matching operation fail.
final class FakeSystem: UpdateSystem {
    var files: [String: Data] = [:]
    var log: [String] = []
    var failOn: (String) -> Bool = { _ in false }
    var links: [String: String] = [:]
    private var temps = 0

    static let newBinary = Data("new binary".utf8)
    static let newApp = Data("new app".utf8)

    func fail(_ op: String) throws {
        log.append(op)
        if failOn(op) { throw UpdateError.install("\(op): Operation not permitted") }
    }

    func makeTempDir() throws -> String {
        temps += 1
        return "/tmp/cs-update-\(temps)"
    }
    func makeDir(_ path: String) throws { try fail("mkdir \(path)") }
    func write(_ data: Data, to path: String) throws {
        try fail("write \(path)")
        files[path] = data
    }
    func read(_ path: String) throws -> Data {
        guard let d = files[path] else { throw UpdateError.install("\(path): no such file") }
        return d
    }
    func exists(_ path: String) -> Bool { files[path] != nil || files.keys.contains { $0.hasPrefix(path + "/") } }
    func resolve(_ path: String) -> String { links[path] ?? path }
    func remove(_ path: String) {
        log.append("rm \(path)")
        for k in files.keys where k == path || k.hasPrefix(path + "/") { files[k] = nil }
    }
    func move(_ from: String, _ to: String) throws {
        try fail("mv \(from) \(to)")
        guard exists(from) else { throw UpdateError.install("\(from): no such file") }
        for k in files.keys where k == from || k.hasPrefix(from + "/") {
            files[to + k.dropFirst(from.count)] = files[k]
            files[k] = nil
        }
    }
    func run(_ argv: [String]) throws {
        try fail(argv.joined(separator: " "))
        switch argv.first {
        case "/usr/bin/tar":  // tar -xzf <file> -C <dir>
            files[argv[4] + "/claudeswitch"] = Self.newBinary
            files[argv[4] + "/LICENSE"] = Data("MIT".utf8)
        case "/usr/bin/ditto":  // ditto -x -k <zip> <dir>
            files[argv[4] + "/ClaudeSwitch.app/Contents/Info.plist"] = Self.newApp
        case "/usr/bin/install":  // install -m 0755 <src> <dst>
            files[argv[4]] = try read(argv[3])
        case "/bin/cp":  // cp -p <src> <dst>
            files[argv[3]] = try read(argv[2])
        default:
            throw UpdateError.install("unexpected command \(argv)")
        }
    }
}

@Suite struct UpdaterTests {
    static let binary = "/Users/person/.local/bin/claudeswitch"
    static let app = "/Applications/ClaudeSwitch.app"
    static let plist = app + "/Contents/Info.plist"
    static let oldBinary = Data("old binary".utf8)
    static let oldApp = Data("old app".utf8)

    func setUp() throws -> (FakeNetwork, FakeSystem, Updater) {
        let net = try FakeNetwork(release: try fixture("github-release-latest.json"))
        let sys = FakeSystem()
        sys.files[Self.binary] = Self.oldBinary
        sys.files[Self.plist] = Self.oldApp
        return (net, sys, Updater(network: net, system: sys, arch: .arm64, repo: "example/claudeswitch"))
    }

    /// Both are exactly as they were, and no backup or temp is left in place
    /// of the app.
    func expectUntouched(_ sys: FakeSystem, sourceLocation: SourceLocation = #_sourceLocation) {
        #expect(sys.files[Self.binary] == Self.oldBinary, "the binary is the old one", sourceLocation: sourceLocation)
        #expect(sys.files[Self.plist] == Self.oldApp, "the app is the old one", sourceLocation: sourceLocation)
        #expect(!sys.files.keys.contains { $0.hasPrefix(Self.app + ".") }, "no backup left beside the app",
                sourceLocation: sourceLocation)
    }

    @Test func testReadsTheLatestReleaseFromTheInjectedRepository() throws {
        let (net, _, up) = try setUp()
        let r = try up.latest().get()
        #expect(r.tag == "v0.5.6")
        #expect(net.asked == ["https://api.github.com/repos/example/claudeswitch/releases/latest"])
    }

    @Test func testInstallsBothRestartsTheDaemonAndCleansUp() throws {
        let (net, sys, up) = try setUp()
        var restarted: [String] = []
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) {
            restarted.append($0)
            return .success(())
        }
        #expect(try r.get() == "0.5.6")
        #expect(sys.files[Self.binary] == FakeSystem.newBinary)
        #expect(sys.files[Self.plist] == FakeSystem.newApp)
        #expect(restarted == [Self.binary], "the daemon restarts on the new binary")
        #expect(sys.log.contains("/usr/bin/install -m 0755 /tmp/cs-update-1/bin/claudeswitch \(Self.binary)"))
        #expect(!sys.files.keys.contains { $0.hasPrefix("/tmp/") }, "the temp directory is removed")
        #expect(!sys.files.keys.contains { $0.hasPrefix(Self.app + ".") }, "the old app is removed")
        #expect(!net.asked.contains { $0.contains("amd64") }, "only this Mac's binary is downloaded")
    }

    @Test func testABinaryBehindALinkIsReplacedWhereItLives() throws {
        let (_, sys, up) = try setUp()
        let real = "/opt/homebrew/Cellar/claudeswitch/0.5.5/bin/claudeswitch"
        sys.files[real] = Self.oldBinary
        sys.files[Self.binary] = nil
        sys.links[Self.binary] = real
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in .success(()) }
        #expect(r.failure == nil)
        #expect(sys.files[real] == FakeSystem.newBinary)
        #expect(sys.files[Self.binary] == nil, "the link itself is not replaced by a file")
    }

    @Test func testATamperedDownloadChangesNothing() throws {
        let (net, sys, up) = try setUp()
        net.served["https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v0.5.6/claudeswitch_v0.5.6_darwin_arm64.tar.gz"]
            = Data("evil".utf8)
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in
            Issue.record("the daemon must not restart")
            return .success(())
        }
        #expect(r.failure == .checksumMismatch("claudeswitch_v0.5.6_darwin_arm64.tar.gz"))
        #expect(!sys.log.contains { $0.hasPrefix("/usr/bin/install") }, "nothing was installed")
        expectUntouched(sys)
    }

    @Test func testATamperedAppZipChangesNothing() throws {
        let (net, sys, up) = try setUp()
        net.served["https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/v0.5.6/ClaudeSwitch-0.5.6-macos.zip"]
            = Data("evil".utf8)
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in .success(()) }
        #expect(r.failure == .checksumMismatch("ClaudeSwitch-0.5.6-macos.zip"))
        expectUntouched(sys)
    }

    @Test func testAFailedDownloadChangesNothing() throws {
        let (net, sys, up) = try setUp()
        net.failing = ["ClaudeSwitch-0.5.6-macos.zip"]
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in .success(()) }
        #expect(r.failure == .download("ClaudeSwitch-0.5.6-macos.zip: offline"))
        expectUntouched(sys)
    }

    @Test func testAFailedBinaryInstallRollsBack() throws {
        let (_, sys, up) = try setUp()
        // The first install (the new binary) fails, as in a directory the
        // app cannot write; the restore is the second and succeeds.
        var installs = 0
        sys.failOn = { op in
            guard op.hasPrefix("/usr/bin/install") else { return false }
            installs += 1
            return installs == 1
        }
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in .success(()) }
        #expect(r.failure?.message.contains("Operation not permitted") == true)
        expectUntouched(sys)
    }

    @Test func testAFailedAppSwapRestoresTheAppAndTheBinary() throws {
        let (_, sys, up) = try setUp()
        sys.failOn = { $0.hasPrefix("mv /tmp/") && $0.hasSuffix(" \(Self.app)") }
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { _ in
            Issue.record("the daemon must not restart before both are in place")
            return .success(())
        }
        #expect(r.failure != nil)
        expectUntouched(sys)
    }

    @Test func testADaemonThatWillNotRestartRollsBothBack() throws {
        let (_, sys, up) = try setUp()
        var restarts: [Data?] = []
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app) { path in
            restarts.append(sys.files[path])
            return restarts.count == 1 ? .failure(.daemon("service_failed: launchctl load failed")) : .success(())
        }
        #expect(r.failure == .daemon("service_failed: launchctl load failed"))
        expectUntouched(sys)
        #expect(restarts == [FakeSystem.newBinary, Self.oldBinary], "restarted again on the restored binary")
    }

    @Test func testErrorsSayWhy() {
        #expect(UpdateError.checksumMismatch("a.zip").message.contains("a.zip"))
        #expect(UpdateError.missingAsset("checksums.txt").message.contains("checksums.txt"))
        #expect(UpdateError.download("x: 404").message.contains("404"))
        #expect(UpdateError.install("y").message.contains("as they were"), "a failure says nothing changed")
    }
}

extension Result where Failure == UpdateError {
    var failure: Failure? {
        if case .failure(let e) = self { return e }
        return nil
    }
}
