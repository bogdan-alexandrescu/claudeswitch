import Testing
@testable import ClaudeSwitchCore

// Owner decision 2026-10-21: `--render` draws the screens to files, and
// SwiftUI still puts its menu bar item up for those seconds. It read "cs …",
// a second, unread ClaudeSwitch; it now says what it is.
@Suite struct RenderLabelTests {
    @Test func testTheRenderingItemSaysItIsRendering() {
        let t = MenuTitle.rendering
        #expect(t.text == "cs render")
        #expect(!t.problem && !t.loading)
    }
}
