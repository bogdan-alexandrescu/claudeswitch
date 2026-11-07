import Foundation

// Next refill (owner's request and picks, 2026-11-06; docs/IMPROVEMENTS.md):
// when an account gives more quota next, and how much. Worked out from the
// readings state.json already holds, so it needs no call and no newer binary.
//
// A window's reset gives back what is used of it. Only what can be spent
// counts: a session reset on an account whose week is shut for days gives
// nothing, which is the usual case once a pool has been worked hard.

/// One reset to come that counts.
public struct Refill: Equatable, Identifiable {
    public var id: String { account + "/" + window }
    public var account: String
    /// "five_hour" or "seven_day".
    public var window: String
    public var at: Date
    /// The points of that window it gives back: what is used of it now.
    public var frees: Double

    public init(account: String, window: String, at: Date, frees: Double) {
        self.account = account
        self.window = window
        self.at = at
        self.frees = frees
    }

    public var isWeek: Bool { window == "seven_day" }
    public var windowWord: String { Format.windowName(window) ?? window }
}

/// One account's picture.
public struct AccountRefills: Equatable, Identifiable {
    public var id: String
    /// Under both triggers and not refused: it can be spent now.
    public var hasRoom: Bool
    /// The resets that count, soonest first.
    public var refills: [Refill]

    public var next: Refill? { refills.first }
}

public enum Refills {
    /// Below this many points a reset is not worth saying: a session at 3%
    /// resetting in ten minutes would otherwise always be next.
    public static let smallest: Double = 5

    /// The picture for one account, against the triggers it is held to.
    public static func of(_ a: AccountView, sessionTrigger: Double, weekTrigger: Double, now: Date) -> AccountRefills {
        // Only a sign-in brings one back; with no reading there is nothing
        // to go by.
        if a.status == .needsLogin || a.status == .unknown { return AccountRefills(id: a.id, hasRoom: false, refills: []) }

        struct Reading {
            var window: String
            var used: Double
            var at: Date?
            var over: Bool
        }
        func read(_ w: WindowReading?, _ key: String, _ trigger: Double) -> Reading? {
            guard let used = w?.utilization, used.isFinite else { return nil }
            // A reset already past: whatever was read, that window is empty.
            if let at = w?.resetsAt, at <= now { return nil }
            return Reading(window: key, used: used, at: w?.resetsAt, over: used >= trigger)
        }
        let windows = [read(a.fiveHour, "five_hour", sessionTrigger), read(a.sevenDay, "seven_day", weekTrigger)]
            .compactMap { $0 }

        // What keeps it shut, and until when; nil is "no time known".
        var shut: [Date?] = windows.filter(\.over).map(\.at)
        var refusal: (window: String?, until: Date?)?
        if case .refused(let w, let until) = a.status {
            refusal = (w, until)
            shut.append(until)
        }
        guard shut.allSatisfy({ $0 != nil }) else { return AccountRefills(id: a.id, hasRoom: false, refills: []) }
        let opens = shut.compactMap { $0 }.max()

        let refills = windows.compactMap { w -> Refill? in
            guard w.used >= smallest, var at = w.at else { return nil }
            // A refused window gives quota back once the refusal clears.
            if refusal?.window == w.window, let until = refusal?.until, until > at { at = until }
            // A reset before the account opens again gives nothing to spend.
            if let opens, at < opens { return nil }
            return Refill(account: a.id, window: w.window, at: at, frees: w.used)
        }
        return AccountRefills(id: a.id, hasRoom: shut.isEmpty, refills: refills.sorted { $0.at < $1.at })
    }

    /// Every account the snapshot knows, each against the profile that judges
    /// it (Snapshot.build's rule: the one it is live in, else the first that
    /// uses it).
    public static func all(_ s: Snapshot) -> [AccountRefills] {
        s.accounts.map { a in
            let c = s.cards.first { $0.active?.id == a.id } ?? s.cards.first { $0.accounts.contains { $0.id == a.id } }
            return of(a, sessionTrigger: c?.switchAt ?? s.settings.trigger(for: "five_hour"),
                      weekTrigger: c?.switchAtWeekly ?? s.settings.trigger(for: "seven_day"), now: s.now)
        }
    }

    /// A card's other accounts: the live one's resets are on its own dials,
    /// just above.
    public static func others(_ c: ProfileCard, now: Date) -> [AccountRefills] {
        c.accounts.filter { $0.id != c.active?.id }
            .map { of($0, sessionTrigger: c.switchAt, weekTrigger: c.switchAtWeekly, now: now) }
    }

    /// Every refill, soonest first.
    public static func upcoming(_ list: [AccountRefills]) -> [Refill] {
        list.flatMap(\.refills).sorted { ($0.at, $0.account) < ($1.at, $1.account) }
    }
}

/// The words, kept out of the views.
public enum RefillText {
    /// "in 1h 36m".
    public static func countdown(_ r: Refill, now: Date) -> String { "in " + Format.duration(r.at.timeIntervalSince(now)) }

    /// "98% of week".
    public static func frees(_ r: Refill) -> String { Format.pct(r.frees) + " of " + r.windowWord }

    /// The popover's line: "Next refill: cedar-max in 48m · 62% of session".
    /// A long name is cut, never the figures. Nil with nothing to come.
    public static func nextLine(_ c: ProfileCard, now: Date) -> String? {
        guard let r = Refills.upcoming(Refills.others(c, now: now)).first else { return nil }
        return "Next refill: \(Format.middle(r.account, 20)) \(countdown(r, now: now)) · \(frees(r))"
    }

    /// The line's tooltip: the next four, named in full, with their clock
    /// times.
    public static func tooltip(_ c: ProfileCard, now: Date, timeZone: TimeZone = .current) -> String? {
        let up = Refills.upcoming(Refills.others(c, now: now))
        guard !up.isEmpty else { return nil }
        return up.prefix(4).map { r in
            "\(r.account): \(frees(r)) \(countdown(r, now: now)) (\(Runway.when(r.at, now: now, timeZone: timeZone)))"
        }.joined(separator: "\n")
    }

    /// The Refills view's subtitle: "7 of 14 have room now · next refill in 48m".
    public static func summary(_ all: [AccountRefills], now: Date) -> String {
        guard !all.isEmpty else { return "No accounts yet" }
        let room = all.filter(\.hasRoom).count
        var t = "\(room) of \(all.count) \(room == 1 ? "has" : "have") room now"
        if let n = Refills.upcoming(all).first { t += " · next refill " + countdown(n, now: now) }
        return t
    }
}

/// How far ahead the timeline looks.
public enum RefillZoom: String, CaseIterable, Identifiable {
    case hours = "5 Hours", day = "1 Day", week = "1 Week"

    public var id: String { rawValue }

    public var seconds: TimeInterval {
        switch self {
        case .hours: return 5 * 3600
        case .day: return 86_400
        case .week: return 7 * 86_400
        }
    }

    /// The axis' labels and how far along each sits, 0 to 1.
    public var ticks: [(label: String, at: Double)] {
        switch self {
        case .hours: return (1...5).map { ("\($0)h", Double($0) / 5) }
        case .day: return [("6h", 0.25), ("12h", 0.5), ("18h", 0.75), ("1d", 1)]
        case .week: return (1...7).map { ("\($0)d", Double($0) / 7) }
        }
    }

    /// How far along the zoom a refill comes, 0 to 1.
    public func position(_ r: Refill, now: Date) -> Double {
        r.at.timeIntervalSince(now) / seconds
    }
}

/// The timeline's amount filter.
public enum RefillAtLeast: Double, CaseIterable, Identifiable {
    case any = 0, quarter = 25, half = 50, most = 75

    public var id: Double { rawValue }
    public var label: String { self == .any ? "Any Amount" : "At Least \(Int(rawValue))%" }
}

/// What the timeline draws under its filters.
public struct RefillTimeline: Equatable {
    /// Soonest first.
    public var events: [Refill]
    /// An account a lane, in the order their first refills come.
    public var lanes: [String]
    /// Refills exist, and the filters hide them all.
    public var canWiden: Bool

    public static func build(_ all: [AccountRefills], zoom: RefillZoom, atLeast: RefillAtLeast, now: Date) -> RefillTimeline {
        let every = Refills.upcoming(all)
        let events = every.filter { $0.frees >= atLeast.rawValue && $0.at.timeIntervalSince(now) <= zoom.seconds }
        var lanes: [String] = []
        for e in events where !lanes.contains(e.account) { lanes.append(e.account) }
        return RefillTimeline(events: events, lanes: lanes, canWiden: events.isEmpty && !every.isEmpty)
    }

    /// What it says with nothing to draw; nil with something.
    public var emptyLine: String? {
        guard events.isEmpty else { return nil }
        return canWiden ? "No refill matches" : "No refill coming"
    }
}
