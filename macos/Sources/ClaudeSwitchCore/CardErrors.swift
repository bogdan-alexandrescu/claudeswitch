import Foundation

/// A refused action's error, shown inline in the popover card that caused it
/// (IMPROVEMENTS M11). The popover never opens a modal alert: one opened from
/// it was never put on screen by macOS and left the popover blocked until the
/// app was relaunched.
public struct CardError: Identifiable, Equatable {
    /// A fresh id per refusal, so the same refusal again shows afresh.
    public let id = UUID()
    public var title: String
    /// The CLI's message, verbatim.
    public var message: String
    public var hint: String
    /// When the CLI said it may clear (retry_at, R1).
    public var retryAt: Date?

    public init(title: String, message: String, hint: String = "", retryAt: Date? = nil) {
        self.title = title
        self.message = message
        self.hint = hint
        self.retryAt = retryAt
    }

    public init(title: String, _ e: CallError) {
        self.init(title: title, message: e.message, hint: e.hint, retryAt: e.retryAt)
    }

    /// "Try again at HH:MM:SS" in the given zone, or nil without retry_at.
    public func retryLine(timeZone: TimeZone = .current) -> String? {
        guard let at = retryAt else { return nil }
        return "Try again at " + Self.clock(at, timeZone: timeZone)
    }

    /// HH:MM:SS, as the CLI's R1 message writes a time.
    public static func clock(_ at: Date, timeZone: TimeZone = .current) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = timeZone
        f.dateFormat = "HH:mm:ss"
        return f.string(from: at)
    }
}

/// The popover's inline errors, one per card (keyed by profile name).
public struct CardErrors: Equatable {
    private var byCard: [String: CardError] = [:]

    public init() {}

    /// Shows e on that card, replacing what it showed.
    public mutating func post(_ e: CardError, card: String) { byCard[card] = e }

    /// The dismiss button.
    public mutating func dismiss(card: String) { byCard[card] = nil }

    /// A new action on the card: its old error no longer applies.
    public mutating func begin(card: String) { byCard[card] = nil }

    public func error(for card: String) -> CardError? { byCard[card] }

    /// How many cards show an error.
    public var count: Int { byCard.count }
}
