import AppKit

/// What the live menu-bar glyph is saying.
public enum GlyphState: Equatable {
    /// The followed profile's live account has room.
    case healthy
    /// It is within reach of its switch threshold: the same drawing.
    case climbing
    /// Rotation is moving the profile to another account: the satellite
    /// sits at the incoming account's week.
    case switching
    /// The live account was refused or needs a login: a warning mark
    /// replaces the satellite.
    case needsYou
}

/// The followed profile's readings, as the menu-bar glyph draws them.
public struct GlyphReading: Equatable {
    public var state: GlyphState
    /// The live account's week and session, 0…1.
    public var week: Double
    public var session: Double
    /// Where the satellite sits on the outer ring, 0…1; nil draws none
    /// (no data yet, or the warning mark instead).
    public var satellite: Double?
    /// VoiceOver's reading of the item.
    public var accessibility: String

    public init(state: GlyphState, week: Double, session: Double, satellite: Double?, accessibility: String) {
        self.state = state
        self.week = week
        self.session = session
        self.satellite = satellite
        self.accessibility = accessibility
    }

    static func fraction(_ w: WindowReading?) -> Double {
        guard let u = w?.utilization, u.isFinite else { return 0 }
        return min(max(u, 0), 100) / 100
    }

    /// The glyph for the profile the menu bar follows (as MenuTitle does).
    public static func of(_ s: Snapshot?, profile: String?) -> GlyphReading {
        let empty = GlyphReading(state: .healthy, week: 0, session: 0, satellite: nil, accessibility: "ClaudeSwitch")
        guard let s else { return empty }
        let card = s.card(s.followed(profile))
        guard let a = card?.active ?? (s.cards.isEmpty ? s.active : nil) else { return empty }
        let prefix = (card != nil && s.cards.count > 1) ? card!.name + " · " : ""
        let week = fraction(a.sevenDay), session = fraction(a.fiveHour)
        var text = "ClaudeSwitch: \(prefix)\(a.id), week \(Format.pct(a.sevenDay?.utilization)), "
            + "session \(Format.pct(a.fiveHour?.utilization))"
        let decision = card?.decision ?? s.decision

        switch a.status {
        case .needsLogin:
            return GlyphReading(state: .needsYou, week: week, session: session, satellite: nil,
                                accessibility: text + ", \(a.id) needs a login")
        case .refused:
            return GlyphReading(state: .needsYou, week: week, session: session, satellite: nil,
                                accessibility: text + ", \(a.id) was refused")
        default: break
        }
        if let d = decision, d.kind == "switch", let t = d.target, t != a.id {
            let incoming = card?.accounts.first { $0.id == t } ?? s.accounts.first { $0.id == t }
            text += ", switching to \(t)"
            if let w = incoming?.sevenDay?.utilization { text += " (week \(Format.pct(w)))" }
            return GlyphReading(state: .switching, week: week, session: session,
                                satellite: fraction(incoming?.sevenDay), accessibility: text)
        }
        if a.level >= .near {
            return GlyphReading(state: .climbing, week: week, session: session, satellite: week,
                                accessibility: text + ", nearing its switch threshold")
        }
        return GlyphReading(state: .healthy, week: week, session: session, satellite: week, accessibility: text)
    }
}

/// The menu-bar image: the rings drawn from a reading, as a template so
/// macOS tints it for light, dark and selected menu bars.
public enum MenuGlyph {
    public static let size = NSSize(width: 18, height: 18)
    /// Dot radius in the 64-unit box, heavier than the icon's so the dots
    /// survive at 18 pt.
    public static let dotRadius: CGFloat = 2.6

    public static func image(_ r: GlyphReading, size: NSSize = MenuGlyph.size) -> NSImage {
        let img = NSImage(size: size, flipped: true) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            draw(r, in: ctx, rect: rect)
            return true
        }
        img.isTemplate = true
        img.accessibilityDescription = r.accessibility
        return img
    }

    /// Draws into a y-down context.
    public static func draw(_ r: GlyphReading, in ctx: CGContext, rect: CGRect, color: CGColor = .black) {
        TwinRings.draw(in: ctx, rect: rect, week: r.week, session: r.session,
                       satellite: r.state == .needsYou ? nil : r.satellite,
                       dotRadius: dotRadius, colors: .mono(color), flipped: true)
        guard r.state == .needsYou else { return }
        ctx.saveGState()
        let s = min(rect.width, rect.height) / TwinRings.box
        ctx.translateBy(x: rect.minX + (rect.width - TwinRings.box * s) / 2,
                        y: rect.minY + (rect.height - TwinRings.box * s) / 2)
        ctx.scaleBy(x: s, y: s)
        let tri = TwinRings.warningPath()
        // Clear a margin round the triangle, then the triangle, then cut
        // the "!" out of it.
        ctx.setBlendMode(.clear)
        ctx.addPath(tri)
        ctx.setLineWidth(6)
        ctx.setLineJoin(.round)
        ctx.drawPath(using: .fillStroke)
        ctx.setBlendMode(.normal)
        ctx.setFillColor(color)
        ctx.addPath(tri)
        ctx.fillPath()
        ctx.setBlendMode(.clear)
        ctx.fill(CGRect(x: 45.6, y: 9.5, width: 2.8, height: 7))
        ctx.fill(CGRect(x: 45.6, y: 17.8, width: 2.8, height: 2.6))
        ctx.restoreGState()
    }
}
