import CryptoKit
import Foundation

// F10: the update checker. Once a day (never when the check is off) the app
// reads the latest GitHub release; when it is newer, Update downloads the app
// zip and this Mac's binary, verifies both against the release's sha256
// files, installs them where the current ones are, restarts the daemon and
// relaunches. Any failure leaves both as they were. The network and the file
// system are seams (UpdateNetwork, UpdateSystem), so the tests never touch
// either.

/// Where releases come from: one constant.
public enum UpdateSource {
    /// The project's GitHub address, written as a full github.com URL so the
    /// public sync rewrites it like every other one (scripts/sync-public.sh).
    public static let home = "https://github.com/bogdan-alexandrescu/claudeswitch"
    /// owner/repo on GitHub, from `home`.
    public static let repo: String = {
        URL(string: home)!.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    }()

    public static func latestURL(repo: String = repo) -> URL {
        URL(string: "https://api.github.com/repos/\(repo)/releases/latest")!
    }
}

/// A release version: major.minor.patch, an optional pre-release after "-"
/// (which orders before its release) and build metadata after "+" (ignored).
public struct SemVer: Comparable, CustomStringConvertible {
    public var major: Int
    public var minor: Int
    public var patch: Int
    public var pre: String?

    public init?(_ text: String) {
        var s = text.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("v") || s.hasPrefix("V") { s.removeFirst() }
        if let plus = s.firstIndex(of: "+") { s = String(s[..<plus]) }
        var pre: String?
        if let dash = s.firstIndex(of: "-") {
            pre = String(s[s.index(after: dash)...])
            s = String(s[..<dash])
            if pre?.isEmpty == true { return nil }
        }
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        guard (2...3).contains(parts.count) else { return nil }
        let nums = parts.map { Int($0) }
        guard nums.allSatisfy({ $0 != nil && $0! >= 0 }) else { return nil }
        major = nums[0]!
        minor = nums[1]!
        patch = nums.count > 2 ? nums[2]! : 0
        self.pre = pre
    }

    public var description: String { "\(major).\(minor).\(patch)" + (pre.map { "-" + $0 } ?? "") }

    public static func < (a: SemVer, b: SemVer) -> Bool {
        if (a.major, a.minor, a.patch) != (b.major, b.minor, b.patch) {
            return (a.major, a.minor, a.patch) < (b.major, b.minor, b.patch)
        }
        switch (a.pre, b.pre) {
        case (nil, nil), (nil, _?): return false
        case (_?, nil): return true
        case let (x?, y?): return x.compare(y, options: .numeric) == .orderedAscending
        }
    }

    public static func == (a: SemVer, b: SemVer) -> Bool {
        (a.major, a.minor, a.patch) == (b.major, b.minor, b.patch) && a.pre == b.pre
    }
}

public struct ReleaseAsset: Equatable {
    public var name: String
    public var url: URL
    public var size: Int?
}

/// GitHub's release object (GET /repos/{owner}/{repo}/releases/latest),
/// only what the updater reads.
public struct Release: Equatable {
    public var tag: String
    public var htmlURL: URL?
    public var draft: Bool
    public var prerelease: Bool
    public var assets: [ReleaseAsset]

    public var version: SemVer? { SemVer(tag) }

    public init?(data: Data) {
        guard let j = JSON(data: data), j.raw is [String: Any], let tag = j["tag_name"].string, !tag.isEmpty else {
            return nil
        }
        self.tag = tag
        htmlURL = j["html_url"].string.flatMap(URL.init(string:))
        draft = j["draft"].bool ?? false
        prerelease = j["prerelease"].bool ?? false
        assets = j["assets"].array.compactMap { a in
            guard let n = a["name"].string, let u = a["browser_download_url"].string.flatMap(URL.init(string:)) else {
                return nil
            }
            return ReleaseAsset(name: n, url: u, size: a["size"].double.map { Int($0) })
        }
    }
}

/// The Mac's architecture as the release names it.
public enum UpdateArch: String {
    case arm64, amd64

    public static var current: UpdateArch {
        #if arch(arm64)
        return .arm64
        #else
        return .amd64
        #endif
    }

    /// The archive's target, as in claudeswitch_<tag>_darwin_arm64.tar.gz.
    public var target: String { "darwin_" + rawValue }
}

/// The four files an update downloads.
public struct UpdatePlan: Equatable {
    public var tarball: ReleaseAsset
    public var checksums: ReleaseAsset
    public var appZip: ReleaseAsset
    public var appSHA: ReleaseAsset
}

public enum UpdateError: Error, Equatable {
    case noRelease(String)
    case missingAsset(String)
    case checksumMissing(String)
    case checksumMismatch(String)
    case download(String)
    case install(String)
    case daemon(String)

    /// What went wrong and that nothing changed.
    public var message: String {
        let kept = " ClaudeSwitch and claudeswitch are as they were."
        switch self {
        case .noRelease(let s): return "Could not read the latest release: \(s)."
        case .missingAsset(let s): return "The release has no \(s)." + kept
        case .checksumMissing(let s): return "The release lists no checksum for \(s)." + kept
        case .checksumMismatch(let s): return "\(s) does not match its published checksum." + kept
        case .download(let s): return "A download failed (\(s))." + kept
        case .install(let s): return "Could not install the update: \(s)." + kept
        case .daemon(let s): return "The daemon did not restart on the new binary (\(s)), so both were put back." + kept
        }
    }
}

extension Release {
    /// The app zip (universal), its .sha256, this Mac's binary archive and
    /// checksums.txt.
    public func plan(arch: UpdateArch) -> Result<UpdatePlan, UpdateError> {
        func named(_ test: (String) -> Bool) -> ReleaseAsset? { assets.first { test($0.name) } }
        guard let tar = named({ $0.hasPrefix("claudeswitch_") && $0.hasSuffix("_\(arch.target).tar.gz") }) else {
            return .failure(.missingAsset("claudeswitch for \(arch.target)"))
        }
        guard let sums = named({ $0 == "checksums.txt" }) else { return .failure(.missingAsset("checksums.txt")) }
        guard let zip = named({ $0.hasPrefix("ClaudeSwitch-") && $0.hasSuffix("-macos.zip") }) else {
            return .failure(.missingAsset("ClaudeSwitch app zip"))
        }
        guard let sha = named({ $0 == zip.name + ".sha256" }) else {
            return .failure(.missingAsset(zip.name + ".sha256"))
        }
        return .success(UpdatePlan(tarball: tar, checksums: sums, appZip: zip, appSHA: sha))
    }
}

public enum Checksums {
    /// `shasum -a 256` lines: "<hex>  <name>" (or "<hex> *<name>"), by name.
    public static func parse(_ text: String) -> [String: String] {
        var out: [String: String] = [:]
        for raw in text.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            let parts = line.split(maxSplits: 1, whereSeparator: { $0 == " " || $0 == "\t" })
            guard parts.count == 2 else { continue }
            let hex = parts[0].lowercased()
            guard hex.count == 64, hex.allSatisfy(\.isHexDigit) else { continue }
            var name = parts[1].trimmingCharacters(in: .whitespaces)
            if name.hasPrefix("*") { name.removeFirst() }
            guard !name.isEmpty else { continue }
            out[name] = hex
        }
        return out
    }

    public static func sha256(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    public static func verify(_ data: Data, name: String, sums: [String: String]) -> Result<Void, UpdateError> {
        guard let want = sums[name] else { return .failure(.checksumMissing(name)) }
        return sha256(data) == want ? .success(()) : .failure(.checksumMismatch(name))
    }
}

public enum UpdateOffer {
    /// The release's version when it is newer than `current` (the app's
    /// CFBundleShortVersionString), else nil. Drafts, pre-releases and
    /// unversioned builds are never offered.
    public static func available(current: String?, latest: Release) -> String? {
        guard !latest.draft, !latest.prerelease, let have = current.flatMap(SemVer.init), let new = latest.version,
              new.pre == nil, new > have else { return nil }
        return new.description
    }

    public static func footer(_ version: String) -> String { "\(version) available" }
}

public enum UpdateSchedule {
    public static let interval: TimeInterval = 24 * 3600

    /// One check a day; none when the check is off.
    public static func due(enabled: Bool, lastCheck: Date?, now: Date) -> Bool {
        guard enabled else { return false }
        guard let last = lastCheck, last <= now else { return true }
        return now.timeIntervalSince(last) >= interval
    }
}

// MARK: seams

public protocol UpdateNetwork {
    /// The body of a GET, or a thrown UpdateError.download.
    func data(from url: URL) throws -> Data
}

public protocol UpdateSystem {
    func makeTempDir() throws -> String
    func makeDir(_ path: String) throws
    func write(_ data: Data, to path: String) throws
    func read(_ path: String) throws -> Data
    func exists(_ path: String) -> Bool
    /// The path with its symbolic links resolved.
    func resolve(_ path: String) -> String
    func remove(_ path: String)
    func move(_ from: String, _ to: String) throws
    /// Runs a command by absolute path; throws on a non-zero exit.
    func run(_ argv: [String]) throws
}

/// Downloads, verifies and installs a release (F10).
public struct Updater {
    public var network: UpdateNetwork
    public var system: UpdateSystem
    public var arch: UpdateArch
    public var repo: String

    public init(network: UpdateNetwork, system: UpdateSystem, arch: UpdateArch = .current,
                repo: String = UpdateSource.repo) {
        self.network = network
        self.system = system
        self.arch = arch
        self.repo = repo
    }

    public func latest() -> Result<Release, UpdateError> {
        let data: Data
        do { data = try network.data(from: UpdateSource.latestURL(repo: repo)) } catch {
            return .failure(.noRelease(Self.describe(error)))
        }
        guard let r = Release(data: data) else { return .failure(.noRelease("GitHub answered with no release")) }
        return .success(r)
    }

    static func describe(_ e: Error) -> String {
        if let u = e as? UpdateError {
            switch u {
            case .download(let s), .install(let s), .noRelease(let s), .daemon(let s): return s
            default: return u.message
            }
        }
        return e.localizedDescription
    }

    /// Installs `release` over `binary` (followed through links to the file
    /// itself) and the bundle at `app`, then restarts the daemon on the
    /// binary. Returns the version installed. On any failure both are put
    /// back as they were (and the daemon restarted on the old binary when
    /// the new one had been put in place).
    public func install(_ release: Release, binary: String, app: String,
                        restartDaemon: (String) -> Result<Void, UpdateError>) -> Result<String, UpdateError> {
        let plan: UpdatePlan
        switch release.plan(arch: arch) {
        case .success(let p): plan = p
        case .failure(let e): return .failure(e)
        }
        let version = release.version?.description ?? release.tag
        let tmp: String
        do { tmp = try system.makeTempDir() } catch { return .failure(.install(Self.describe(error))) }
        defer { system.remove(tmp) }

        // 1. Download and verify everything before touching anything.
        let tarPath = tmp + "/" + plan.tarball.name
        let zipPath = tmp + "/" + plan.appZip.name
        do {
            let sums = Checksums.parse(String(decoding: try network.data(from: plan.checksums.url), as: UTF8.self))
            let appSums = Checksums.parse(String(decoding: try network.data(from: plan.appSHA.url), as: UTF8.self))
            let tar = try network.data(from: plan.tarball.url)
            try Checksums.verify(tar, name: plan.tarball.name, sums: sums).get()
            let zip = try network.data(from: plan.appZip.url)
            try Checksums.verify(zip, name: plan.appZip.name, sums: appSums).get()
            try system.write(tar, to: tarPath)
            try system.write(zip, to: zipPath)
        } catch let e as UpdateError {
            return .failure(e)
        } catch {
            return .failure(.download(Self.describe(error)))
        }

        // 2. Unpack both beside each other in the temp directory.
        let binDir = tmp + "/bin", appDir = tmp + "/app"
        let newBinary = binDir + "/claudeswitch"
        let appName = (app as NSString).lastPathComponent
        do {
            try system.makeDir(binDir)
            try system.run(["/usr/bin/tar", "-xzf", tarPath, "-C", binDir])
            try system.makeDir(appDir)
            try system.run(["/usr/bin/ditto", "-x", "-k", zipPath, appDir])
        } catch {
            return .failure(.install(Self.describe(error)))
        }
        guard system.exists(newBinary) else { return .failure(.install("the archive holds no claudeswitch binary")) }
        guard let newApp = ["ClaudeSwitch.app", appName].map({ appDir + "/" + $0 }).first(where: system.exists) else {
            return .failure(.install("the zip holds no ClaudeSwitch.app"))
        }

        // 3. The binary, where the current one is, keeping a copy.
        let target = system.resolve(binary)
        let backup = tmp + "/claudeswitch.previous"
        do {
            try system.run(["/bin/cp", "-p", target, backup])
        } catch {
            return .failure(.install(Self.describe(error)))
        }
        func restoreBinary() { try? system.run(["/usr/bin/install", "-m", "0755", backup, target]) }
        do {
            try system.run(["/usr/bin/install", "-m", "0755", newBinary, target])
        } catch {
            restoreBinary()
            return .failure(.install(Self.describe(error)))
        }

        // 4. The app: the old bundle aside, the new one in its place.
        let aside = app + ".previous"
        system.remove(aside)
        do {
            try system.move(app, aside)
        } catch {
            restoreBinary()
            return .failure(.install(Self.describe(error)))
        }
        func restoreApp() {
            if system.exists(app) { system.remove(app) }
            try? system.move(aside, app)
        }
        do {
            try system.move(newApp, app)
        } catch {
            restoreApp()
            restoreBinary()
            return .failure(.install(Self.describe(error)))
        }

        // 5. The daemon on the new binary; if it will not start, both go back.
        if case .failure(let e) = restartDaemon(target) {
            restoreApp()
            restoreBinary()
            _ = restartDaemon(target)
            return .failure(e)
        }
        system.remove(aside)
        return .success(version)
    }
}
