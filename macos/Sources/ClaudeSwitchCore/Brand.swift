import AppKit

/// The app's Appearance setting: System follows the Mac, Light and Dark
/// hold the app in one appearance (popover and every window).
public enum AppearancePref: String, CaseIterable, Identifiable {
    case system, light, dark

    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    /// What NSApp.appearance is set to; nil follows the system.
    public var appearanceName: NSAppearance.Name? {
        switch self {
        case .system: return nil
        case .light: return .aqua
        case .dark: return .darkAqua
        }
    }

    public var appearance: NSAppearance? { appearanceName.flatMap(NSAppearance.init(named:)) }
}

/// How the popover shows session and week.
public enum UsageMode: String, CaseIterable, Identifiable {
    case dials, bars

    public var id: String { rawValue }
    public var label: String { self == .dials ? "Dials" : "Bars" }
}

/// The app's own preferences that live in its defaults (the rest of its
/// settings are claudeswitch's, in config.toml).
public struct AppPrefs {
    public static let appearanceKey = "appearance"
    public static let usageModeKey = "usageMode"

    let defaults: UserDefaults

    public init(_ defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    public var appearance: AppearancePref {
        get { defaults.string(forKey: Self.appearanceKey).flatMap(AppearancePref.init(rawValue:)) ?? .system }
        nonmutating set { defaults.set(newValue.rawValue, forKey: Self.appearanceKey) }
    }

    public var usageMode: UsageMode {
        get { defaults.string(forKey: Self.usageModeKey).flatMap(UsageMode.init(rawValue:)) ?? .dials }
        nonmutating set { defaults.set(newValue.rawValue, forKey: Self.usageModeKey) }
    }
}

/// The Twin rings palette (docs/BRAND.md). The UI tokens are dynamic: one
/// value for light appearances and one for dark.
public enum Palette {
    // The brand's fixed colours, for the app icon and brand art.
    public static let graphite = rgb(0x17181b)
    public static let frost = rgb(0xf4f7fa)
    public static let glacier = rgb(0x8fd3ff)
    public static let glacierInk = rgb(0x2a9fe0)
    public static let meltwater = rgb(0xb6f0dc)

    // The UI tokens, light / dark.
    public static let accent = dynamic("csAccent", light: 0x2a9fe0, dark: 0x8fd3ff)
    public static let accent2 = dynamic("csAccent2", light: 0x3fbf98, dark: 0xb6f0dc)
    public static let track = dynamic("csTrack", light: 0xe1e7ed, dark: 0x31363d)
    public static let tick = dynamic("csTick", light: 0x17181b, dark: 0xb6f0dc)
    public static let onAccent = dynamic("csOnAccent", light: 0xffffff, dark: 0x0d1b24)

    public static func rgb(_ hex: UInt32, alpha: CGFloat = 1) -> NSColor {
        NSColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255,
                blue: CGFloat(hex & 0xff) / 255, alpha: alpha)
    }

    public static func dynamic(_ name: String, light: UInt32, dark: UInt32) -> NSColor {
        let l = rgb(light), d = rgb(dark)
        return NSColor(name: NSColor.Name(name)) { ap in isDark(ap) ? d : l }
    }

    public static func isDark(_ ap: NSAppearance) -> Bool {
        ap.bestMatch(from: [.aqua, .darkAqua, .vibrantLight, .vibrantDark,
                            .accessibilityHighContrastAqua, .accessibilityHighContrastDarkAqua,
                            .accessibilityHighContrastVibrantLight, .accessibilityHighContrastVibrantDark])
            .map { [.darkAqua, .vibrantDark, .accessibilityHighContrastDarkAqua,
                    .accessibilityHighContrastVibrantDark].contains($0) } ?? false
    }
}

/// The dot maths of the popover's dials and bars.
public enum Dots {
    public static let dialCount = 30
    public static let barCount = 25

    /// Dots lit on an arc of `count`: the dot at t = i / (count − 1) is lit
    /// when t is at most the reading. Nothing is lit for no use or an
    /// unknown reading.
    public static func litOnArc(_ pct: Double?, count: Int) -> Int {
        guard let p = pct, p.isFinite, p > 0, count > 1 else { return 0 }
        let f = min(p, 100) / 100
        return min(count, Int((f * Double(count - 1) + 1e-9).rounded(.down)) + 1)
    }

    /// The threshold's dot on an arc: the one nearest the trigger.
    public static func arcThreshold(_ trigger: Double, count: Int) -> Int {
        let f = min(max(trigger, 0), 100) / 100
        return Int((f * Double(count - 1)).rounded())
    }

    /// Dots lit on a bar: one per 100 / count points, rounded.
    public static func litOnBar(_ pct: Double?, count: Int) -> Int {
        guard let p = pct, p.isFinite, p > 0 else { return 0 }
        return min(count, Int((min(p, 100) * Double(count) / 100).rounded()))
    }

    /// The threshold's dot on a bar: the last dot at or before the trigger.
    public static func barThreshold(_ trigger: Double, count: Int) -> Int {
        let i = Int((trigger * Double(count) / 100).rounded()) - 1
        return min(max(i, 0), count - 1)
    }
}

/// The Twin rings mark: the week as 30 dots on an outer 270° arc, the
/// 5-hour session as 19 on an inner one, and a satellite at the week's
/// reading. Geometry in a 64-unit box, y down, as in the brand SVG.
public enum TwinRings {
    public static let box: CGFloat = 64
    public static let center = CGPoint(x: 32, y: 33)
    public static let outerRadius: CGFloat = 25
    public static let innerRadius: CGFloat = 15.5
    public static let outerCount = 30
    public static let innerCount = 19
    public static let satelliteRadius: CGFloat = 5
    /// Unlit dots are the lit colour's family at this opacity.
    public static let unlitOpacity: CGFloat = 0.28

    /// The point at `fraction` of the arc: 135° plus fraction × 270°.
    public static func point(radius: CGFloat, fraction: Double) -> CGPoint {
        let a = (135 + 270 * min(max(fraction, 0), 1)) * .pi / 180
        return CGPoint(x: center.x + radius * CGFloat(cos(a)), y: center.y + radius * CGFloat(sin(a)))
    }

    public static func dots(radius: CGFloat, count: Int) -> [CGPoint] {
        (0..<count).map { point(radius: radius, fraction: Double($0) / Double(count - 1)) }
    }

    public struct Colors {
        public var outer: CGColor
        public var inner: CGColor
        /// Unlit dots, drawn at `unlitAlpha`.
        public var unlit: CGColor
        public var unlitAlpha: CGFloat

        public init(outer: CGColor, inner: CGColor, unlit: CGColor, unlitAlpha: CGFloat = TwinRings.unlitOpacity) {
            self.outer = outer
            self.inner = inner
            self.unlit = unlit
            self.unlitAlpha = unlitAlpha
        }

        /// One colour for everything: a template image.
        public static func mono(_ c: CGColor) -> Colors { Colors(outer: c, inner: c, unlit: c) }

        /// The app icon's: frost unlit, glacier week, meltwater session.
        public static var icon: Colors {
            Colors(outer: Palette.glacier.cgColor, inner: Palette.meltwater.cgColor, unlit: Palette.frost.cgColor)
        }
    }

    /// Draws the mark into `rect` (fitted, centred). `flipped` says the
    /// context is already y down.
    public static func draw(in ctx: CGContext, rect: CGRect, week: Double, session: Double, satellite: Double?,
                            dotRadius: CGFloat = 2.1, satelliteRadius: CGFloat = TwinRings.satelliteRadius,
                            colors: Colors, flipped: Bool = false) {
        ctx.saveGState()
        defer { ctx.restoreGState() }
        let s = min(rect.width, rect.height) / box
        let ox = rect.minX + (rect.width - box * s) / 2
        if flipped {
            ctx.translateBy(x: ox, y: rect.minY + (rect.height - box * s) / 2)
            ctx.scaleBy(x: s, y: s)
        } else {
            ctx.translateBy(x: ox, y: rect.maxY - (rect.height - box * s) / 2)
            ctx.scaleBy(x: s, y: -s)
        }
        func arc(_ r: CGFloat, _ n: Int, _ f: Double, _ lit: CGColor) {
            let on = Dots.litOnArc(f * 100, count: n)
            for (i, p) in dots(radius: r, count: n).enumerated() {
                if i < on {
                    ctx.setFillColor(lit)
                } else {
                    ctx.setFillColor(colors.unlit.copy(alpha: colors.unlit.alpha * colors.unlitAlpha) ?? colors.unlit)
                }
                ctx.fillEllipse(in: CGRect(x: p.x - dotRadius, y: p.y - dotRadius, width: 2 * dotRadius, height: 2 * dotRadius))
            }
        }
        arc(outerRadius, outerCount, week, colors.outer)
        arc(innerRadius, innerCount, session, colors.inner)
        if let f = satellite {
            let p = point(radius: outerRadius, fraction: f)
            ctx.setFillColor(colors.outer)
            ctx.fillEllipse(in: CGRect(x: p.x - satelliteRadius, y: p.y - satelliteRadius,
                                       width: 2 * satelliteRadius, height: 2 * satelliteRadius))
        }
    }

    /// The warning mark that replaces the satellite when the live account
    /// needs you: a triangle at the top right, its "!" cut out, with a clear
    /// margin around it so it reads apart from the dots under it. Same
    /// 64-unit, y-down space as `draw`; call it inside the same transform.
    public static func warningPath() -> CGPath {
        let p = CGMutablePath()
        p.move(to: CGPoint(x: 47, y: 4))
        p.addLine(to: CGPoint(x: 58, y: 22))
        p.addLine(to: CGPoint(x: 36, y: 22))
        p.closeSubpath()
        return p
    }
}

/// The app icon: the mark on a dark radial graphite tile.
public enum AppIcon {
    /// Draws a `size`-pixel icon into a y-up context: the tile inset as
    /// Apple's grid has it (824 of 1024), then the rings.
    public static func draw(in ctx: CGContext, size: CGFloat) {
        let inset = size * 100 / 1024
        let tile = CGRect(x: inset, y: inset, width: size - 2 * inset, height: size - 2 * inset)
        let radius = tile.width * 0.2237
        let shape = CGPath(roundedRect: tile, cornerWidth: radius, cornerHeight: radius, transform: nil)

        // A soft drop shadow under the tile, as macOS icons have.
        ctx.saveGState()
        ctx.setShadow(offset: CGSize(width: 0, height: -size * 0.01), blur: size * 0.025,
                      color: CGColor(gray: 0, alpha: 0.45))
        ctx.addPath(shape)
        ctx.setFillColor(Palette.rgb(0x0e0f11).cgColor)
        ctx.fillPath()
        ctx.restoreGState()

        // radial-gradient(100% 100% at 30% 20%, #2a2e34, #0e0f11)
        ctx.saveGState()
        ctx.addPath(shape)
        ctx.clip()
        let space = CGColorSpace(name: CGColorSpace.sRGB)!
        let g = CGGradient(colorsSpace: space,
                           colors: [Palette.rgb(0x2a2e34).cgColor, Palette.rgb(0x0e0f11).cgColor] as CFArray,
                           locations: [0, 1])!
        let c = CGPoint(x: tile.minX + tile.width * 0.3, y: tile.maxY - tile.height * 0.2)
        ctx.drawRadialGradient(g, startCenter: c, startRadius: 0, endCenter: c, endRadius: tile.width * 1.05,
                               options: [.drawsAfterEndLocation])
        // A hairline inner edge.
        ctx.addPath(shape)
        ctx.setStrokeColor(CGColor(gray: 1, alpha: 0.07))
        ctx.setLineWidth(max(1, size / 512))
        ctx.strokePath()
        ctx.restoreGState()

        // The mark at 66% of the tile, as in the brand sheet (74 of 112).
        let m = tile.width * 74 / 112
        let rect = CGRect(x: tile.midX - m / 2, y: tile.midY - m / 2, width: m, height: m)
        // Small sizes get fatter dots so the rings survive at 16 px.
        let dot: CGFloat = size <= 32 ? 2.6 : 2.1
        TwinRings.draw(in: ctx, rect: rect, week: 0.62, session: 0.3, satellite: 0.62, dotRadius: dot,
                       colors: .icon)
    }

    /// The iconset's files: name and pixel size.
    public static let iconset: [(name: String, pixels: Int)] = [16, 32, 128, 256, 512].flatMap { pt in
        [("icon_\(pt)x\(pt).png", pt), ("icon_\(pt)x\(pt)@2x.png", pt * 2)]
    }

    public static func png(pixels: Int) -> Data? {
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels,
                                         bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                         colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
              let ns = NSGraphicsContext(bitmapImageRep: rep) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = ns
        ns.cgContext.clear(CGRect(x: 0, y: 0, width: pixels, height: pixels))
        draw(in: ns.cgContext, size: CGFloat(pixels))
        NSGraphicsContext.restoreGraphicsState()
        return rep.representation(using: .png, properties: [:])
    }
}
