import Foundation

/// A lenient view over parsed JSON.
///
/// The binary's output and state.json are written by Go code that moves on
/// independently of this app. A field that is missing, null, renamed or of an
/// unexpected type must degrade to "unknown" rather than fail the whole read,
/// so nothing here throws: every accessor returns an optional.
public struct JSON {
    public let raw: Any?

    public init(_ raw: Any?) { self.raw = raw }

    /// Parses bytes; nil when they are not JSON at all.
    public init?(data: Data) {
        guard let v = try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) else {
            return nil
        }
        raw = v
    }

    public subscript(key: String) -> JSON {
        JSON((raw as? [String: Any])?[key])
    }

    public var isNull: Bool { raw == nil || raw is NSNull }

    public var string: String? { raw as? String }

    public var double: Double? {
        // NSNumber bridges a JSON bool too; refuse it so true never reads as 1.
        if let n = raw as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID() { return n.doubleValue }
        if let s = raw as? String { return Double(s.trimmingCharacters(in: .whitespaces)) }
        return nil
    }

    public var bool: Bool? {
        if let n = raw as? NSNumber, CFGetTypeID(n) == CFBooleanGetTypeID() { return n.boolValue }
        return nil
    }

    public var date: Date? { string.flatMap(GoTime.parse) }

    public var array: [JSON] { (raw as? [Any])?.map(JSON.init) ?? [] }

    public var object: [String: JSON] {
        (raw as? [String: Any])?.mapValues(JSON.init) ?? [:]
    }

    /// An object's keys, sorted, so iteration order is stable.
    public var keys: [String] { (raw as? [String: Any])?.keys.sorted() ?? [] }
}

/// Parsing for Go's time.Time JSON encoding (RFC 3339 with up to nine
/// fractional digits and either "Z" or a numeric offset).
public enum GoTime {
    /// Parses a timestamp. Go's zero time ("0001-01-01T00:00:00Z") means "never"
    /// and comes back as nil, because Go before 1.24 writes it in place of an
    /// omitted field.
    public static func parse(_ s: String) -> Date? {
        var text = s.trimmingCharacters(in: .whitespaces)
        guard text.count >= 20 else { return nil }
        // Split off the fraction, which ISO8601DateFormatter only reads at
        // millisecond precision on some systems.
        var fraction: Double = 0
        if let dot = text.firstIndex(of: "."), text.distance(from: text.startIndex, to: dot) == 19 {
            var end = text.index(after: dot)
            while end < text.endIndex, text[end].isNumber { end = text.index(after: end) }
            let digits = String(text[text.index(after: dot)..<end])
            fraction = Double("0." + digits) ?? 0
            text.removeSubrange(dot..<end)
        }
        guard let base = formatter.date(from: text) else { return nil }
        if base.timeIntervalSince1970 < -62_000_000_000 { return nil } // year 1: zero time
        return base.addingTimeInterval(fraction)
    }

    private static let formatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()
}
