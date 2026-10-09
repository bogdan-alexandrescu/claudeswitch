import AppKit
import ClaudeSwitchCore
import CoreText
import SwiftUI

/// The Twin rings UI tokens as SwiftUI colours (dynamic: light and dark).
extension Color {
    static let csAccent = Color(nsColor: Palette.accent)
    static let csAccent2 = Color(nsColor: Palette.accent2)
    static let csTrack = Color(nsColor: Palette.track)
    static let csTick = Color(nsColor: Palette.tick)
    static let csOnAccent = Color(nsColor: Palette.onAccent)
}

/// The tokens resolved for a colour scheme, for Canvas drawing.
struct BrandTokens {
    let accent, accent2, track, tick: Color

    init(_ scheme: ColorScheme) {
        let ap = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)!
        func r(_ c: NSColor) -> Color {
            var out = c
            ap.performAsCurrentDrawingAppearance { out = c.usingColorSpace(.sRGB) ?? c }
            return Color(nsColor: out)
        }
        accent = r(Palette.accent)
        accent2 = r(Palette.accent2)
        track = r(Palette.track)
        tick = r(Palette.tick)
    }
}

/// Geist, the wordmark's face (SIL OFL 1.1, Resources/Geist-OFL.txt).
/// Only the wordmark uses it; the UI stays SF Pro.
enum BrandFont {
    static let postScriptName = "Geist-SemiBold"
    private(set) static var registered = false

    /// Registers the bundled Geist-SemiBold for this process: from the app
    /// bundle's Resources, or from the source tree when run from SwiftPM's
    /// build directory (--render, swift run).
    static func register() {
        guard !registered else { return }
        let source = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("Resources/Geist-SemiBold.otf")
        let candidates = [Bundle.main.url(forResource: "Geist-SemiBold", withExtension: "otf"), source]
        for url in candidates.compactMap({ $0 }) where FileManager.default.fileExists(atPath: url.path) {
            var error: Unmanaged<CFError>?
            if CTFontManagerRegisterFontsForURL(url as CFURL, .process, &error) || NSFont(name: postScriptName, size: 12) != nil {
                registered = true
                return
            }
        }
        menuBarLog.error("could not register Geist-SemiBold; the wordmark falls back to SF Pro")
    }

    /// Geist 600 when it is registered, else SF Pro semibold.
    static func wordmark(_ size: CGFloat) -> Font {
        NSFont(name: postScriptName, size: size) != nil ? .custom(postScriptName, size: size)
            : .system(size: size, weight: .semibold)
    }
}

/// The Twin rings mark in the UI's colours: lit week dots in accent, lit
/// session dots in accent2, unlit in track, an optional threshold dot per
/// ring in tick, and the satellite at the week's reading.
struct TwinRingsView: View {
    var week: Double
    var session: Double
    var satellite = true
    /// In the 64-unit box.
    var dotRadius: CGFloat = 2.3
    var satelliteRadius: CGFloat = 5.2
    /// Threshold fractions (0…1), drawn as a larger tick-coloured dot.
    var weekThreshold: Double?
    var sessionThreshold: Double?
    /// Override the lit colours (a warning, say).
    var outer: Color?
    var inner: Color?
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let t = BrandTokens(scheme)
        Canvas { ctx, size in
            let s = min(size.width, size.height) / TwinRings.box
            let ox = (size.width - TwinRings.box * s) / 2, oy = (size.height - TwinRings.box * s) / 2
            func dot(_ p: CGPoint, _ r: CGFloat, _ c: Color) {
                ctx.fill(Path(ellipseIn: CGRect(x: ox + (p.x - r) * s, y: oy + (p.y - r) * s, width: 2 * r * s, height: 2 * r * s)),
                         with: .color(c))
            }
            func arc(_ radius: CGFloat, _ n: Int, _ f: Double, _ lit: Color, _ thr: Double?) {
                let on = Dots.litOnArc(f * 100, count: n)
                let ti = thr.map { Dots.arcThreshold($0 * 100, count: n) }
                for (i, p) in TwinRings.dots(radius: radius, count: n).enumerated() {
                    if i == ti {
                        dot(p, dotRadius * 1.5, t.tick)
                    } else {
                        dot(p, dotRadius, i < on ? lit : t.track)
                    }
                }
            }
            arc(TwinRings.outerRadius, TwinRings.outerCount, week, outer ?? t.accent, weekThreshold)
            arc(TwinRings.innerRadius, TwinRings.innerCount, session, inner ?? t.accent2, sessionThreshold)
            if satellite {
                dot(TwinRings.point(radius: TwinRings.outerRadius, fraction: week), satelliteRadius, outer ?? t.accent)
            }
        }
        .accessibilityHidden(true)
    }
}

/// A small pair of rings for an account: its week and session, no
/// satellite when there is no reading.
struct MiniRings: View {
    let account: AccountView?
    var size: CGFloat = 26
    var dotRadius: CGFloat = 2.8

    var body: some View {
        let w = (account?.sevenDay?.utilization ?? 0) / 100
        let s = (account?.fiveHour?.utilization ?? 0) / 100
        TwinRingsView(week: w, session: s, satellite: w > 0, dotRadius: dotRadius, satelliteRadius: 5.2)
            .frame(width: size, height: size)
    }
}

/// The mark and "claudeswitch" in Geist 600, tightly tracked.
struct Wordmark: View {
    var size: CGFloat = 15
    var mark: CGFloat = 20

    var body: some View {
        HStack(spacing: size * 0.47) {
            TwinRingsView(week: 0.62, session: 0.3, dotRadius: 2.6)
                .frame(width: mark, height: mark)
            Text("claudeswitch")
                .font(BrandFont.wordmark(size))
                .tracking(-0.04 * size)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("claudeswitch")
    }
}

/// The primary action: an accent fill with onAccent text, so it reads in
/// dark mode, where the accent is light.
struct PrimaryButtonStyle: ButtonStyle {
    var capsule = false
    @Environment(\.isEnabled) private var enabled
    @Environment(\.controlSize) private var controlSize

    func makeBody(configuration: Configuration) -> some View {
        let v: CGFloat = controlSize == .large ? 7 : controlSize == .small ? 2 : 4
        let h: CGFloat = controlSize == .large ? 12 : controlSize == .small ? 8 : 10
        return configuration.label
            .font(controlSize == .large ? .body.weight(.semibold) : .body.weight(.medium))
            .foregroundStyle(Color.csOnAccent)
            .padding(.vertical, v).padding(.horizontal, h)
            .background(shape.fill(Color.csAccent))
            .opacity(enabled ? (configuration.isPressed ? 0.75 : 1) : 0.45)
            .contentShape(shape)
    }

    var shape: some InsettableShape { RoundedRectangle(cornerRadius: capsule ? 100 : 6, style: .continuous) }
}

/// The popover's pair of pills: the primary in accent, the other quiet.
struct PillButtonStyle: ButtonStyle {
    let primary: Bool
    @Environment(\.isEnabled) private var enabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(primary ? Color.csOnAccent : Color.primary)
            .padding(.vertical, 7).padding(.horizontal, 12)
            .background(PillShape().fill(primary ? Color.csAccent
                : Color.secondary.opacity(configuration.isPressed ? 0.22 : 0.12)))
            .overlay(primary ? nil : PillShape(inset: 0.5).stroke(Color.secondary.opacity(0.2), lineWidth: 1))
            .opacity(enabled ? (primary && configuration.isPressed ? 0.8 : 1) : 0.45)
            .contentShape(PillShape())
    }
}

/// A true stadium: circular ends. SwiftUI's Capsule draws continuous
/// corners, which on a tall pill flatten its ends and leave the stroke
/// as a stray straight line at each side.
struct PillShape: InsettableShape {
    var inset: CGFloat = 0

    func path(in r: CGRect) -> Path {
        let b = r.insetBy(dx: inset, dy: inset)
        return Path(roundedRect: b, cornerRadius: min(b.width, b.height) / 2, style: .circular)
    }

    func inset(by amount: CGFloat) -> PillShape { PillShape(inset: inset + amount) }
}

/// One usage window as a dot dial: 30 dots lit to the reading, the
/// profile's threshold as a larger tick-coloured dot, a satellite at the
/// reading, the figure in the middle, and the label and reset below.
struct DialView: View {
    let title: String
    let window: WindowReading?
    let trigger: Double
    let now: Date
    var size: CGFloat = 96
    @Environment(\.colorScheme) private var scheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var pct: Double? { window?.utilization }

    var body: some View {
        let t = BrandTokens(scheme)
        let lit = litColor(t)
        VStack(spacing: 1) {
            ZStack {
                Canvas { ctx, sz in
                    let s = min(sz.width, sz.height) / TwinRings.box
                    func dot(_ p: CGPoint, _ r: CGFloat, _ c: Color) {
                        ctx.fill(Path(ellipseIn: CGRect(x: (p.x - r) * s, y: (p.y - r) * s, width: 2 * r * s, height: 2 * r * s)),
                                 with: .color(c))
                    }
                    let n = Dots.dialCount
                    let on = Dots.litOnArc(pct, count: n)
                    let ti = Dots.arcThreshold(trigger, count: n)
                    for (i, p) in TwinRings.dots(radius: TwinRings.outerRadius, count: n).enumerated() {
                        if i == ti { dot(p, 3.2, t.tick) } else { dot(p, 1.9, i < on ? lit : t.track) }
                    }
                    if let p = pct, p.isFinite {
                        dot(TwinRings.point(radius: TwinRings.outerRadius, fraction: min(max(p, 0), 100) / 100), 3.8, lit)
                    }
                }
                Text(Format.pct(pct))
                    .font(.system(size: size * 14 / 64, weight: .semibold))
                    .monospacedDigit()
                    .offset(y: size * 1.5 / 64)
            }
            .frame(width: size, height: size)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.35), value: pct)
            Text(title).font(.caption.weight(.semibold))
            Text(Format.resets(window?.resetsAt, now: now).ifEmpty("–"))
                .font(.caption2).foregroundStyle(.secondary).monospacedDigit()
        }
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(title): \(Format.pct(pct))\(resetText), switches at \(Int(trigger))%")
    }

    var resetText: String {
        let r = Format.resets(window?.resetsAt, now: now)
        return r.isEmpty ? "" : ", " + r
    }

    /// The accent while there is room; warnings keep their semantic colours.
    func litColor(_ t: BrandTokens) -> Color {
        guard let p = pct else { return t.accent }
        switch Level.of(p, trigger: trigger) {
        case .ok: return t.accent
        case .near: return .orange
        case .over: return .red
        }
    }
}

/// One usage window as a row of 25 dots, the threshold dot larger.
struct DotBarView: View {
    let title: String
    let window: WindowReading?
    let trigger: Double
    let now: Date

    var pct: Double? { window?.utilization }

    /// Dot diameter, in points.
    static let dot: CGFloat = 7

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title)
                Spacer()
                Text(detail).foregroundStyle(.secondary).monospacedDigit()
            }
            .font(.caption)
            let n = Dots.barCount
            let on = Dots.litOnBar(pct, count: n)
            let ti = Dots.barThreshold(trigger, count: n)
            // Each dot has an explicit diameter. A bare Circle has no size of
            // its own, so in the popover (which sizes to its content) the dots
            // collapsed to nothing; only the fixed-size renders showed them.
            HStack(spacing: 0) {
                ForEach(0..<n, id: \.self) { i in
                    Circle()
                        .fill(i == ti ? Color.csTick : i < on ? lit : Color.csTrack)
                        .frame(width: Self.dot, height: Self.dot)
                        .scaleEffect(i == ti ? 1.3 : 1)
                        .frame(maxWidth: .infinity)
                }
            }
            .frame(height: Self.dot * 1.3)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(title): \(detail), switches at \(Int(trigger))%")
    }

    var detail: String {
        let r = Format.resets(window?.resetsAt, now: now)
        return Format.pct(pct) + (r.isEmpty ? "" : " · " + r)
    }

    var lit: Color {
        guard let p = pct else { return .csAccent }
        return levelColor(Level.of(p, trigger: trigger))
    }
}

/// Session and week, as dials or bars (the popover's switch).
struct UsageView: View {
    @EnvironmentObject var store: Store
    let account: AccountView
    let card: ProfileCard
    let now: Date

    var body: some View {
        switch store.usageMode {
        case .dials:
            HStack(spacing: 0) {
                DialView(title: "Session", window: account.fiveHour, trigger: card.switchAt, now: now)
                DialView(title: "Week", window: account.sevenDay, trigger: card.switchAtWeekly, now: now)
            }
        case .bars:
            VStack(alignment: .leading, spacing: 9) {
                DotBarView(title: "Session", window: account.fiveHour, trigger: card.switchAt, now: now)
                DotBarView(title: "Week", window: account.sevenDay, trigger: card.switchAtWeekly, now: now)
            }
        }
    }
}

/// The popover's small dials / bars switch.
struct UsageModeToggle: View {
    @Binding var mode: UsageMode

    var body: some View {
        HStack(spacing: 0) {
            segment(.dials) { DialGlyph().stroke(style: StrokeStyle(lineWidth: 1.7, lineCap: .round)) }
            Rectangle().fill(Color.secondary.opacity(0.3)).frame(width: 1)
            segment(.bars) { BarsGlyph().stroke(style: StrokeStyle(lineWidth: 1.9, lineCap: .round)) }
        }
        .fixedSize()
        .background(RoundedRectangle(cornerRadius: 7).fill(Color(nsColor: .controlBackgroundColor)))
        .clipShape(RoundedRectangle(cornerRadius: 7))
        .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.secondary.opacity(0.3)))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Show usage as")
    }

    func segment<S: View>(_ m: UsageMode, @ViewBuilder _ icon: () -> S) -> some View {
        Button { mode = m } label: {
            icon()
                .frame(width: 15, height: 15)
                .padding(.vertical, 3).padding(.horizontal, 7)
                .foregroundStyle(mode == m ? Color.csAccent : Color.secondary)
                .background(mode == m ? Color.csAccent.opacity(0.14) : Color.clear)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(m.label)
        .accessibilityLabel(m.label)
        .accessibilityAddTraits(mode == m ? .isSelected : [])
    }
}

/// Two open arcs: the dials icon (16-unit box).
struct DialGlyph: Shape {
    func path(in r: CGRect) -> Path {
        let s = min(r.width, r.height) / 16
        var p = Path()
        p.addArc(center: CGPoint(x: r.minX + 8 * s, y: r.minY + 8 * s), radius: 6 * s,
                 startAngle: .degrees(135), endAngle: .degrees(45), clockwise: false)
        p.move(to: CGPoint(x: r.minX + (8 + 3 * cos(.pi * 0.75)) * s, y: r.minY + (8 + 3 * sin(.pi * 0.75)) * s))
        p.addArc(center: CGPoint(x: r.minX + 8 * s, y: r.minY + 8 * s), radius: 3 * s,
                 startAngle: .degrees(135), endAngle: .degrees(45), clockwise: false)
        return p
    }
}

/// Two lines: the bars icon.
struct BarsGlyph: Shape {
    func path(in r: CGRect) -> Path {
        let s = min(r.width, r.height) / 16
        var p = Path()
        p.move(to: CGPoint(x: r.minX + 2.5 * s, y: r.minY + 5.5 * s))
        p.addLine(to: CGPoint(x: r.minX + 13.5 * s, y: r.minY + 5.5 * s))
        p.move(to: CGPoint(x: r.minX + 2.5 * s, y: r.minY + 10.5 * s))
        p.addLine(to: CGPoint(x: r.minX + 9.5 * s, y: r.minY + 10.5 * s))
        return p
    }
}

extension String {
    func ifEmpty(_ s: String) -> String { isEmpty ? s : self }
}
