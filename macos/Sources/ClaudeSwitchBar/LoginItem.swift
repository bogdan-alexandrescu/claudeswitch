import Foundation
import ServiceManagement

/// Launch at login through SMAppService (macOS 13+). It registers the app
/// bundle itself, so it only works from an installed .app, not `swift run`.
@MainActor
final class LoginItem: ObservableObject {
    @Published private(set) var enabled = false
    @Published private(set) var error: String?

    /// False when running outside an app bundle, where there is nothing to
    /// register.
    let available = Bundle.main.bundleIdentifier != nil && Bundle.main.bundlePath.hasSuffix(".app")

    init() { reload() }

    func reload() {
        guard available else { return }
        enabled = SMAppService.mainApp.status == .enabled
    }

    func set(_ on: Bool) {
        guard available else { return }
        error = nil
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            self.error = error.localizedDescription
        }
        if SMAppService.mainApp.status == .requiresApproval {
            self.error = "Allow it in System Settings → General → Login Items."
        }
        reload()
    }
}
