import CoreGraphics
import Foundation

/// Where the menu-bar item sits. macOS places a new status item to the left
/// of the others, and on a MacBook with a notch and a full menu bar that can
/// be under the notch, where it is drawn but cannot be seen (the owner's 14"
/// MacBook, 1512-pt menu bar: x=685–840). AppKit keeps each item's position,
/// measured from the right edge of the screen, in the app's defaults under
/// `NSStatusItem Preferred Position Item-0`; a ⌘-drag writes it there too.
public enum StatusItemPlacement {
    public static let preferredPositionKey = "NSStatusItem Preferred Position Item-0"
    /// Points from the right edge: right of the notch on every notched Mac.
    public static let defaultPosition: Double = 300

    /// On a first launch (nothing stored), stores defaultPosition so AppKit
    /// places the item near the right edge. A stored position — the
    /// person's own ⌘-drag — is never overwritten. Returns whether it seeded.
    /// Must run before the status item is created.
    @discardableResult
    public static func seed(_ defaults: UserDefaults) -> Bool {
        guard defaults.object(forKey: preferredPositionKey) == nil else { return false }
        defaults.set(defaultPosition, forKey: preferredPositionKey)
        return true
    }

    /// Whether an item's frame falls into the notch: the gap between the
    /// screen's auxiliary top-left and top-right areas, on a screen whose
    /// safe area has a top inset. No inset, or an unknown gap, claims nothing.
    public static func hiddenByNotch(item: CGRect, safeAreaTop: CGFloat, left: CGRect?, right: CGRect?) -> Bool {
        guard safeAreaTop > 0, let l = left, let r = right, r.minX > l.maxX else { return false }
        return item.maxX > l.maxX && item.minX < r.minX
    }
}
