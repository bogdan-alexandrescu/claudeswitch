import AppKit
import ClaudeSwitchCore
import OSLog
import SwiftUI
import UserNotifications

let flowsLog = Logger(subsystem: "xyz.claudeswitch.menubar", category: "flows")

/// The app's long-lived parts, reachable from AppKit callbacks (URL events,
/// the hotkey, notification actions) as well as from the views.
@MainActor
enum AppHub {
    static let store = Store()
    static let updates = UpdateController()
    static let notifier = Notifier()
    static let hotKey = HotKeyCenter()

    /// Wires the flows to the store; once, at launch (not in --render).
    static func start() {
        let store = self.store
        notifier.start()
        store.didRebuild = { s in
            notifier.observe(s, store: store)
            ShortcutRunner.flush()
        }
        store.didFirstRead = { path in
            if FirstRun.shouldOffer(configPath: path) {
                flowsLog.notice("no config: showing the setup window")
                WelcomeWindow.show(store: store)
            }
        }
        hotKey.action = { ShortcutRunner.run(.switchBest(profile: nil)) }
        hotKey.apply()
        updates.binary = { store.binaryPath }
        updates.start()
    }
}

/// Runs a claudeswitch:// URL or the hotkey (F6). A command that arrives
/// before the first read (the URL launched the app) waits for it.
@MainActor
enum ShortcutRunner {
    private static var pending: [ShortcutCommand] = []

    static func run(_ c: ShortcutCommand) {
        let store = AppHub.store
        guard let s = store.snapshot else {
            pending.append(c)
            return
        }
        let name = c.profile ?? s.followed(store.menuProfile)
        guard let card = s.card(name) else {
            AppHub.notifier.inform("There is no profile called \(name).")
            return
        }
        switch c {
        case .switchBest:
            if let b = card.best, !card.alreadyOnBest {
                store.use(b.id, profile: card.name)
            } else {
                AppHub.notifier.inform("\(card.name) is already on its best account. " + (card.bestWhy ?? ""))
            }
        case .pin:
            guard let a = card.active?.id else {
                AppHub.notifier.inform("\(card.name) has no live account to pin.")
                return
            }
            store.setPinned(true, profile: card.name, account: a)
        case .unpin:
            store.setPinned(false, profile: card.name, account: nil)
        case .status:
            AppHub.notifier.inform(StatusLine.text(card))
        }
    }

    static func flush() {
        guard !pending.isEmpty else { return }
        let p = pending
        pending = []
        p.forEach(run)
    }
}

/// The app's one notification router and UNUserNotificationCenter delegate
/// (there can be only one): it registers every category (NoticeCategory.all:
/// F11's rotation and sign-in, F5's re-login reminder) and dispatches every
/// action through NoticeRouter. It also posts F11's notices: the daemon
/// notifies through osascript (`display notification`), which shows under
/// Script Editor and cannot carry actions, so the app posts the actionable
/// one when it sees the change in state.json. F5's ReloginNotifier posts its
/// reminders and leaves registration and routing here.
@MainActor
final class Notifier: NSObject, ObservableObject, UNUserNotificationCenterDelegate {
    static let enabledKey = "notifyWithActions"

    @Published var enabled: Bool {
        didSet { UserDefaults.standard.set(enabled, forKey: Self.enabledKey) }
    }
    private var last: NoticeState?

    override init() {
        enabled = UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool ?? true
        super.init()
    }

    /// UNUserNotificationCenter needs a bundle; `swift run` has none.
    var available: Bool {
        Bundle.main.bundleURL.pathExtension == "app" && Bundle.main.bundleIdentifier != nil
            && !CommandLine.arguments.contains("--render")
    }

    func start() {
        guard available else { return }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        let categories = NoticeCategory.all.map { id in
            UNNotificationCategory(identifier: id,
                                   actions: NoticeCategory.actions(id).map {
                                       // A sign-in opens Settings: bring the app forward.
                                       UNNotificationAction(identifier: $0.id, title: $0.title,
                                                            options: $0.title.hasPrefix("Sign in") ? [.foreground] : [])
                                   },
                                   intentIdentifiers: [], options: [])
        }
        center.setNotificationCategories(Set(categories))
        center.requestAuthorization(options: [.alert, .sound]) { granted, error in
            if let error { flowsLog.error("notifications: \(error.localizedDescription, privacy: .public)") }
            flowsLog.notice("notifications allowed: \(granted, privacy: .public)")
        }
    }

    /// Compares a new snapshot with the last and posts what changed.
    func observe(_ s: Snapshot, store: Store) {
        let now = NoticeState(s)
        defer { last = now }
        guard enabled, available else { return }
        for n in Notices.diff(old: last, new: now, appSwitches: store.appSwitches, now: s.now) { post(n) }
    }

    func post(_ n: Notice) {
        let c = UNMutableNotificationContent()
        c.title = n.title
        c.body = n.body
        c.categoryIdentifier = n.category
        c.userInfo = n.info
        c.sound = .default
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: n.id, content: c, trigger: nil))
    }

    /// A plain notification: a shortcut's answer (Status, or why nothing
    /// was done). Without notifications, a beep.
    func inform(_ text: String) {
        guard available else {
            NSSound.beep()
            return
        }
        let c = UNMutableNotificationContent()
        c.title = "ClaudeSwitch"
        c.body = text
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "info:\(UUID().uuidString)",
                                                                     content: c, trigger: nil))
    }

    func handle(category: String, action: String, identifier: String, userInfo: [AnyHashable: Any]) {
        guard let cmd = NoticeRouter.command(category: category, action: action, identifier: identifier,
                                             userInfo: userInfo) else { return }
        let store = AppHub.store
        switch cmd {
        case let .use(account, profile):
            store.use(account, profile: profile)
        case .pin(let account):
            store.setPinned(true, profile: store.pool(of: account) ?? StateFile.defaultProfile, account: account)
        case let .signIn(account, _):
            // Add account → sign in again, on Settings (SettingsOpener).
            store.beginSignIn(account)
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let action = response.actionIdentifier
        let request = response.notification.request
        let category = request.content.categoryIdentifier, identifier = request.identifier
        let info = request.content.userInfo as? [String: String] ?? [:]
        Task { @MainActor in
            self.handle(category: category, action: action, identifier: identifier, userInfo: info)
            completionHandler()
        }
    }

    /// The app is an accessory, so it counts as active more often than not:
    /// show the banner anyway.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }
}
