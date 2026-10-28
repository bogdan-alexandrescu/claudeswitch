import AppKit
import Testing
@testable import ClaudeSwitchCore

/// The brand pass the owner picked on 2026-10-28 (docs/BRAND.md): light
/// mode's accent and its warning, good and bad inks read at 4.5:1, the
/// least small text needs; dark mode keeps its colours, and its switch-point
/// tick is frost, not the mint the low end of the ramp is close to.
@Suite struct BrandContrastTests {
    static let light = NSAppearance(named: .aqua)!
    static let dark = NSAppearance(named: .darkAqua)!

    func resolved(_ c: NSColor, _ ap: NSAppearance) -> NSColor {
        var out = c
        ap.performAsCurrentDrawingAppearance { out = c.usingColorSpace(.sRGB) ?? c }
        return out
    }

    /// WCAG 2 contrast ratio.
    func ratio(_ a: NSColor, _ b: NSColor) -> Double {
        func lum(_ c: NSColor) -> Double {
            func lin(_ v: CGFloat) -> Double {
                let v = Double(v)
                return v <= 0.04045 ? v / 12.92 : pow((v + 0.055) / 1.055, 2.4)
            }
            return 0.2126 * lin(c.redComponent) + 0.7152 * lin(c.greenComponent) + 0.0722 * lin(c.blueComponent)
        }
        let (x, y) = (lum(a), lum(b))
        return (max(x, y) + 0.05) / (min(x, y) + 0.05)
    }

    @Test func testLightAccentCarriesWhiteTextAndReadsAsText() {
        let accent = resolved(Palette.accent, Self.light)
        #expect(ratio(resolved(Palette.onAccent, Self.light), accent) >= 4.5)
        #expect(ratio(accent, Palette.rgb(0xffffff)) >= 4.5)
        // The cards' grey in the popover.
        #expect(ratio(accent, Palette.rgb(0xf5f5f5)) >= 4.5)
    }

    @Test func testLightInksReadOnWhiteAndOnTheirChips() {
        let white = Palette.rgb(0xffffff)
        for (ink, chip) in [(Palette.warn, NSColor.systemOrange), (Palette.good, .systemGreen), (Palette.bad, .systemRed)] {
            let i = resolved(ink, Self.light)
            #expect(ratio(i, white) >= 4.5, "\(ink)")
            // A status chip: the system colour at 18% over white behind the ink.
            let c = resolved(chip, Self.light)
            let fill = NSColor(srgbRed: c.redComponent * 0.18 + 0.82, green: c.greenComponent * 0.18 + 0.82,
                               blue: c.blueComponent * 0.18 + 0.82, alpha: 1)
            #expect(ratio(i, fill) >= 4.5, "\(ink) on its chip")
        }
    }

    @Test func testDarkModeKeepsTheSystemColoursAndItsAccent() {
        for (ink, system) in [(Palette.warn, NSColor.systemOrange), (Palette.good, .systemGreen), (Palette.bad, .systemRed)] {
            #expect(resolved(ink, Self.dark) == resolved(system, Self.dark), "\(ink)")
        }
        #expect(resolved(Palette.accent, Self.dark) == Palette.rgb(0x8fd3ff))
        #expect(resolved(Palette.onAccent, Self.dark) == Palette.rgb(0x0d1b24))
    }

    @Test func testTheDarkTickIsFrostAndTheSessionLineIsGreen() {
        #expect(resolved(Palette.tick, Self.dark) == Palette.frost)
        #expect(resolved(Palette.tick, Self.light) == Palette.graphite)
        #expect(Palette.sessionLine == Palette.rgb(0x3fbf98))
    }
}
