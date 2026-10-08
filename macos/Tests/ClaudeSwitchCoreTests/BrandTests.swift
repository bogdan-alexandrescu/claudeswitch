import AppKit
import Foundation
import Testing
@testable import ClaudeSwitchCore

/// A throwaway defaults domain, so the tests never touch the app's own.
private func scratchDefaults(_ name: String = #function) -> UserDefaults {
    let suite = "claudeswitch.tests.brand." + name
    let d = UserDefaults(suiteName: suite)!
    d.removePersistentDomain(forName: suite)
    return d
}

/// The Appearance setting (System / Light / Dark): stored by the app,
/// applied as NSApp.appearance.
@Suite struct AppearanceTests {
    @Test func testTheDefaultIsSystem() {
        let prefs = AppPrefs(scratchDefaults())
        #expect(prefs.appearance == .system)
    }

    @Test func testItPersists() {
        let d = scratchDefaults()
        AppPrefs(d).appearance = .dark
        #expect(AppPrefs(d).appearance == .dark, "a new reader of the same defaults sees it")
        AppPrefs(d).appearance = .light
        #expect(AppPrefs(d).appearance == .light)
        #expect(d.string(forKey: AppPrefs.appearanceKey) == "light")
    }

    @Test func testAnUnknownStoredValueFallsBackToSystem() {
        let d = scratchDefaults()
        d.set("sepia", forKey: AppPrefs.appearanceKey)
        #expect(AppPrefs(d).appearance == .system)
    }

    @Test func testItMapsToNSAppearance() {
        #expect(AppearancePref.system.appearanceName == nil, "nil: follow the system")
        #expect(AppearancePref.light.appearanceName == .aqua)
        #expect(AppearancePref.dark.appearanceName == .darkAqua)
        #expect(AppearancePref.system.appearance == nil)
        #expect(AppearancePref.dark.appearance?.name == .darkAqua)
        #expect(AppearancePref.light.appearance?.name == .aqua)
        #expect(AppearancePref.allCases.map(\.label) == ["System", "Light", "Dark"])
    }
}

/// Dials or bars in the popover.
@Suite struct UsageModeTests {
    @Test func testTheDefaultIsDials() {
        #expect(AppPrefs(scratchDefaults()).usageMode == .dials)
    }

    @Test func testItPersists() {
        let d = scratchDefaults()
        AppPrefs(d).usageMode = .bars
        #expect(AppPrefs(d).usageMode == .bars)
        #expect(d.string(forKey: AppPrefs.usageModeKey) == "bars")
        AppPrefs(d).usageMode = .dials
        #expect(AppPrefs(d).usageMode == .dials)
        d.set("pie", forKey: AppPrefs.usageModeKey)
        #expect(AppPrefs(d).usageMode == .dials, "an unknown value falls back to dials")
        #expect(UsageMode.allCases.map(\.label) == ["Dials", "Bars"])
    }
}

/// The dot maths of the dials (30 dots on a 270° arc) and the bars (25
/// dots), as the mockup draws them.
@Suite struct DotMathTests {
    @Test func testDialLitDots() {
        // A dot at t = i / 29 is lit when t <= the reading.
        #expect(Dots.litOnArc(18, count: 30) == 6)
        #expect(Dots.litOnArc(41, count: 30) == 12)
        #expect(Dots.litOnArc(100, count: 30) == 30)
        #expect(Dots.litOnArc(150, count: 30) == 30, "clamped")
        #expect(Dots.litOnArc(0, count: 30) == 0, "nothing used, nothing lit")
        #expect(Dots.litOnArc(-3, count: 30) == 0)
        #expect(Dots.litOnArc(nil, count: 30) == 0, "an unknown reading lights nothing")
        #expect(Dots.litOnArc(1, count: 30) == 1, "any use lights the first dot")
        #expect(Dots.litOnArc(30, count: 19) == 6, "the inner ring: 19 dots")
    }

    @Test func testDialThresholdIndex() {
        // The dot nearest the trigger: round(trigger / 100 × 29).
        #expect(Dots.arcThreshold(85, count: 30) == 25)
        #expect(Dots.arcThreshold(98, count: 30) == 28)
        #expect(Dots.arcThreshold(90, count: 30) == 26)
        #expect(Dots.arcThreshold(100, count: 30) == 29)
        #expect(Dots.arcThreshold(0, count: 30) == 0)
        #expect(Dots.arcThreshold(120, count: 30) == 29, "clamped")
    }

    @Test func testBarLitDots() {
        // 4 points a dot: round(reading / 4).
        #expect(Dots.litOnBar(18, count: 25) == 5)
        #expect(Dots.litOnBar(41, count: 25) == 10)
        #expect(Dots.litOnBar(100, count: 25) == 25)
        #expect(Dots.litOnBar(130, count: 25) == 25)
        #expect(Dots.litOnBar(0, count: 25) == 0)
        #expect(Dots.litOnBar(nil, count: 25) == 0)
    }

    @Test func testBarThresholdIndex() {
        #expect(Dots.barThreshold(85, count: 25) == 20)
        #expect(Dots.barThreshold(98, count: 25) == 24)
        #expect(Dots.barThreshold(90, count: 25) == 22)
        #expect(Dots.barThreshold(1, count: 25) == 0, "never before the first dot")
        #expect(Dots.barThreshold(200, count: 25) == 24, "never past the last")
    }

    @Test func testTheRingsGeometry() {
        // Two 270° arcs from 135°, centred at (32, 33) in a 64-unit box.
        let outer = TwinRings.dots(radius: TwinRings.outerRadius, count: TwinRings.outerCount)
        let inner = TwinRings.dots(radius: TwinRings.innerRadius, count: TwinRings.innerCount)
        #expect(outer.count == 30 && inner.count == 19)
        #expect(TwinRings.outerRadius == 25 && TwinRings.innerRadius == 15.5)
        let first = outer[0], last = outer[29]
        #expect(abs(first.x - (32 - 25 * 0.7071)) < 0.01 && abs(first.y - (33 + 25 * 0.7071)) < 0.01,
                "starts at 135°, bottom left (y down)")
        #expect(abs(last.x - (32 + 25 * 0.7071)) < 0.01 && abs(last.y - first.y) < 0.01, "ends at 45°, bottom right")
        let top = TwinRings.point(radius: 25, fraction: 0.5)
        #expect(abs(top.x - 32) < 0.01 && abs(top.y - 8) < 0.01, "half way is straight up")
    }
}

/// The live menu-bar glyph's state, chosen from a snapshot.
@Suite struct GlyphStateTests {
    @Test func testHealthy() throws {
        let s = try cardsSnapshot()
        let g = GlyphReading.of(s, profile: "default")
        #expect(g.state == .healthy)
        #expect(abs(g.week - 0.41) < 0.001 && abs(g.session - 0.03) < 0.001)
        #expect(g.satellite.map { abs($0 - 0.41) < 0.001 } == true, "the satellite sits at the week's reading")
        #expect(g.accessibility == "ClaudeSwitch: default · work-1, week 41%, session 3%")
    }

    @Test func testClimbing() throws {
        var s = try cardsSnapshot()
        let i = try #require(s.cards.firstIndex { $0.name == "default" })
        s.cards[i].active?.level = .near
        let g = GlyphReading.of(s, profile: "default")
        #expect(g.state == .climbing)
        #expect(g.satellite.map { abs($0 - 0.41) < 0.001 } == true, "the same drawing as healthy")
        #expect(g.accessibility.hasSuffix(", nearing its switch threshold"))
    }

    @Test func testSwitching() throws {
        var s = try cardsSnapshot()
        let i = try #require(s.cards.firstIndex { $0.name == "default" })
        s.cards[i].decision?.kind = "switch"
        s.cards[i].decision?.target = "work-2"
        let g = GlyphReading.of(s, profile: "default")
        #expect(g.state == .switching)
        #expect(abs(g.week - 0.41) < 0.001, "the rings still show the live account")
        #expect(g.satellite.map { abs($0 - 0.77) < 0.001 } == true, "the satellite is at the incoming account's week")
        #expect(g.accessibility.hasSuffix(", switching to work-2 (week 77%)"))
    }

    @Test func testNeedsYouWhenTheLiveAccountNeedsALogin() throws {
        let s = try cardsSnapshot()
        let g = GlyphReading.of(s, profile: "review")
        #expect(g.state == .needsYou, "personal's last poll was a 401")
        #expect(g.satellite == nil, "a warning mark instead of the satellite")
        #expect(g.accessibility.hasSuffix(", personal needs a login"))
    }

    @Test func testNeedsYouWhenTheLiveAccountIsRefused() throws {
        var s = try cardsSnapshot()
        let i = try #require(s.cards.firstIndex { $0.name == "default" })
        s.cards[i].active?.status = .refused(window: "five_hour", until: nil)
        #expect(GlyphReading.of(s, profile: "default").state == .needsYou)
        #expect(GlyphReading.of(s, profile: "default").accessibility.hasSuffix(", work-1 was refused"))
    }

    @Test func testNeedsYouWinsOverSwitching() throws {
        var s = try cardsSnapshot()
        let i = try #require(s.cards.firstIndex { $0.name == "default" })
        s.cards[i].active?.status = .needsLogin
        s.cards[i].decision?.kind = "switch"
        s.cards[i].decision?.target = "work-2"
        #expect(GlyphReading.of(s, profile: "default").state == .needsYou)
    }

    @Test func testNoDataIsEmptyRings() {
        let g = GlyphReading.of(nil, profile: nil)
        #expect(g.state == .healthy && g.week == 0 && g.session == 0 && g.satellite == nil)
        #expect(g.accessibility == "ClaudeSwitch")
    }

    @Test func testItFollowsTheChosenProfile() throws {
        let s = try cardsSnapshot()
        #expect(GlyphReading.of(s, profile: "gone").state == .healthy, "falls back to default")
        #expect(GlyphReading.of(s, profile: nil).accessibility.contains("work-1"))
    }
}

/// The menu-bar image is a template, so macOS tints it for every menu bar.
@Suite struct MenuGlyphTests {
    @Test func testItIsATemplateAtMenuBarSize() throws {
        let s = try cardsSnapshot()
        for p in ["default", "review"] {
            let img = MenuGlyph.image(GlyphReading.of(s, profile: p))
            #expect(img.isTemplate)
            #expect(img.size == NSSize(width: 18, height: 18))
            #expect(img.accessibilityDescription?.hasPrefix("ClaudeSwitch") == true)
        }
    }

    @Test func testItDraws() throws {
        let img = MenuGlyph.image(GlyphReading.of(try cardsSnapshot(), profile: "default"))
        let rep = try #require(bitmap(img, scale: 2))
        var opaque = 0
        for x in 0..<rep.pixelsWide { for y in 0..<rep.pixelsHigh where (rep.colorAt(x: x, y: y)?.alphaComponent ?? 0) > 0.5 { opaque += 1 } }
        #expect(opaque > 20, "lit dots and the satellite are drawn")
    }
}

/// Draws an image into a bitmap, to count what it painted.
private func bitmap(_ img: NSImage, scale: CGFloat) -> NSBitmapImageRep? {
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
