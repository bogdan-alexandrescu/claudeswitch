import AppKit
import Testing
@testable import ClaudeSwitchCore

/// The rings as revised on 2026-10-21 (docs/BRAND.md): the 5-hour session on
/// the outer ring and the week on the inner one, each ring's lit dots
/// coloured by its own use on a ramp from green through amber to red, and
/// the satellite on the inner ring, at the week's reading.
@Suite struct UsageRingsTests {
    @Test func testTheRampRunsGreenThroughAmberToRed() {
        #expect(hex(Palette.usage(0, dark: true)) == 0x3fbf98)
        #expect(hex(Palette.usage(0.5, dark: true)) == 0xf0c75e)
        #expect(hex(Palette.usage(1, dark: true)) == 0xff7a7a)
        #expect(hex(Palette.usage(-0.2, dark: true)) == 0x3fbf98, "below 0% is green")
        #expect(hex(Palette.usage(1.7, dark: true)) == 0xff7a7a, "over 100% is red")
        // A quarter of the way is a yellow-green, half way round the hue
        // circle from green to amber, and as saturated as they are: never
        // the olive a red-green-blue blend passes through.
        for dark in [true, false] {
            let (g, a, q) = (hsb(Palette.usage(0, dark: dark)), hsb(Palette.usage(0.5, dark: dark)),
                             hsb(Palette.usage(0.25, dark: dark)))
            #expect(abs(q.h - (g.h + a.h) / 2) < 0.01, "hue half way (dark: \(dark))")
            #expect(q.s >= min(g.s, a.s) - 0.01, "no loss of saturation (dark: \(dark))")
        }
    }

    /// The dark-background stops are too light for a white popover or a light
    /// menu bar, so light backgrounds get darker ones.
    @Test func testOnLightBackgroundsTheRampIsDarker() {
        for f in [0, 0.5, 1.0] {
            #expect(luminance(Palette.usage(f, dark: false)) < luminance(Palette.usage(f, dark: true)), "at \(f)")
        }
    }

    @Test func testTheOuterRingIsTheSession() throws {
        let rep = try draw(week: 0, session: 1)
        #expect(lit(rep, ring: TwinRings.outerRadius, at: 0.5) == 0xff7a7a, "a full session lights the outer ring red")
        #expect(lit(rep, ring: TwinRings.innerRadius, at: 0.5) == nil, "an unused week leaves the inner ring unlit")
    }

    @Test func testTheInnerRingIsTheWeek() throws {
        let rep = try draw(week: 1, session: 0)
        #expect(lit(rep, ring: TwinRings.innerRadius, at: 0.5) == 0xff7a7a)
        #expect(lit(rep, ring: TwinRings.outerRadius, at: 0.5) == nil)
    }

    @Test func testEachRingIsColouredByItsOwnUse() throws {
        let rep = try draw(week: 1, session: 0.5)
        #expect(lit(rep, ring: TwinRings.outerRadius, at: 0) == 0xf0c75e, "session at 50%: amber")
        #expect(lit(rep, ring: TwinRings.innerRadius, at: 0) == 0xff7a7a, "week at 100%: red")
    }

    /// The satellite marks the week, so it rides the inner ring, in the
    /// week's colour; nothing is drawn beside the outer ring's dots.
    @Test func testTheSatelliteRidesTheInnerRingAtTheWeek() throws {
        let rep = try draw(week: 0.5, session: 0, satellite: 0.5)
        // 3.5 units inside a ring's top dot: clear of every dot (radius 2.1),
        // within a satellite there (radius 5).
        let inner = sample(rep, at: TwinRings.point(radius: TwinRings.innerRadius - 3.5, fraction: 0.5))
        #expect(inner.a > 0.9 && hex(inner.color) == 0xf0c75e, "the satellite, amber, on the inner ring")
        let outer = sample(rep, at: TwinRings.point(radius: TwinRings.outerRadius - 3.5, fraction: 0.5))
        #expect(outer.a < 0.1, "no satellite on the outer ring")
    }

    /// In the menu bar the rings are drawn in colour, not as a template that
    /// macOS would tint one colour.
    @Test func testTheMenuGlyphIsInColour() throws {
        let r = GlyphReading(state: .healthy, week: 0.1, session: 1, satellite: 0.1, accessibility: "ClaudeSwitch")
        for dark in [false, true] {
            let img = MenuGlyph.image(r, dark: dark)
            #expect(!img.isTemplate)
            let rep = try #require(bitmapOf(img, scale: 4))
            let s = CGFloat(rep.pixelsWide) / TwinRings.box
            let p = TwinRings.point(radius: TwinRings.outerRadius, fraction: 0.5)
            let c = try #require(rep.colorAt(x: Int(p.x * s), y: Int(p.y * s))?.usingColorSpace(.sRGB))
            #expect(c.alphaComponent > 0.9 && c.redComponent > c.greenComponent + 0.2,
                    "a full session draws the outer ring red (dark menu bar: \(dark))")
        }
    }
}

// MARK: helpers

private func rgb(_ c: NSColor) -> (r: Int, g: Int, b: Int) {
    let s = c.usingColorSpace(.sRGB) ?? c
    return (Int((s.redComponent * 255).rounded()), Int((s.greenComponent * 255).rounded()),
            Int((s.blueComponent * 255).rounded()))
}

private func hex(_ c: NSColor) -> UInt32 {
    let v = rgb(c)
    return UInt32(v.r) << 16 | UInt32(v.g) << 8 | UInt32(v.b)
}

private func hsb(_ c: NSColor) -> (h: CGFloat, s: CGFloat, b: CGFloat) {
    var h: CGFloat = 0, s: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
    (c.usingColorSpace(.sRGB) ?? c).getHue(&h, saturation: &s, brightness: &b, alpha: &a)
    return (h, s, b)
}

private func luminance(_ c: NSColor) -> Double {
    let v = rgb(c)
    return 0.2126 * Double(v.r) + 0.7152 * Double(v.g) + 0.0722 * Double(v.b)
}

/// The mark drawn in the dark-background ramp, unlit dots in white, into a
/// 256-pixel bitmap (4 pixels to the 64-unit box's unit).
private func draw(week: Double, session: Double, satellite: Double? = nil) throws -> NSBitmapImageRep {
    let rep = try #require(NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 256, pixelsHigh: 256, bitsPerSample: 8,
                                            samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0))
    let ns = try #require(NSGraphicsContext(bitmapImageRep: rep))
    TwinRings.draw(in: ns.cgContext, rect: CGRect(x: 0, y: 0, width: 256, height: 256), week: week, session: session,
                   satellite: satellite, colors: .usage(dark: true, unlit: CGColor(gray: 1, alpha: 1)))
    return rep
}

/// The pixel at a point of the 64-unit box (bitmap rows run top down, as
/// the box's y does).
private func sample(_ rep: NSBitmapImageRep, at p: CGPoint) -> (color: NSColor, a: CGFloat) {
    let c = rep.colorAt(x: Int(p.x * 4), y: Int(p.y * 4))?.usingColorSpace(.sRGB) ?? .clear
    return (c, c.alphaComponent)
}

/// A lit dot's colour at `fraction` of a ring, or nil when the dot there is
/// unlit (drawn faint) or missing.
private func lit(_ rep: NSBitmapImageRep, ring radius: CGFloat, at fraction: Double) -> UInt32? {
    let s = sample(rep, at: TwinRings.point(radius: radius, fraction: fraction))
    return s.a > 0.9 ? hex(s.color) : nil
}

private func bitmapOf(_ img: NSImage, scale: CGFloat) -> NSBitmapImageRep? {
    let w = Int(img.size.width * scale), h = Int(img.size.height * scale)
    guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8,
                                     samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                     colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
          let ctx = NSGraphicsContext(bitmapImageRep: rep) else { return nil }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = ctx
    img.draw(in: NSRect(x: 0, y: 0, width: w, height: h))
    NSGraphicsContext.restoreGraphicsState()
    return rep
}
