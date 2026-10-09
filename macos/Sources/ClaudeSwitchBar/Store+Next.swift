import AppKit
@preconcurrency import ClaudeSwitchCore
import Foundation
import SwiftUI
import UserNotifications

/// What F3, F5, F7 and F12 add to the store (docs/IMPROVEMENTS.md, "Next
/// features"). Each is read on demand and degrades on an older binary.
struct NextState {
    /// F7: `history --usage --days 30 --json`.
    var history: UsageHistory?
    var historyError: CallError?
    /// F12: `doctor --json`.
    var doctor: DoctorReport?
    var doctorError: CallError?
    var doctorAt: Date?
    /// The fix that ran last and what came of it, per check.
    var fixNotes: [String: String] = [:]
    var fixErrors: [String: CallError] = [:]
    /// F3: the newest unpin per profile, from the audit log.
    var pinLifts: [String: PinLift] = [:]
    var dismissedLifts: Set<String> = Set(UserDefaults.standard.stringArray(forKey: "dismissedPinLifts") ?? [])
    /// Asks the Settings window to open on a pane (a notification's action).
    var settingsRequest: SettingsRequest?
}

struct SettingsRequest: Equatable {
    let id = UUID()
    var pane: Pane
}

extension Store {
    // MARK: F7 history

    func loadHistory() {
        guard !preview else { return }
        Task {
            switch await call("load:history", { $0.usageHistory(days: HistoryChart.days) }) {
            case .success(let h):
                next.history = h
                next.historyError = nil
            case .failure(let e):
                next.historyError = e
            }
        }
    }

    // MARK: F12 health

    func runDoctor() {
        guard !preview else { return }
        Task {
            switch await call("doctor", { $0.doctor() }) {
            case .success(let d):
                next.doctor = d
                next.doctorError = nil
                next.doctorAt = Date()
            case .failure(let e):
                next.doctorError = e
            }
        }
    }

    /// Runs a check's fix: sign-in opens Add account; the others run the
    /// CLI, then the checks run again.
    func runFix(_ check: DoctorCheck) {
        guard let fix = check.fix else { return }
        next.fixErrors[check.id] = nil
        next.fixNotes[check.id] = nil
        if case .signin(let a) = fix {
            addAccount = AddAccountRequest(account: a, profile: pool(of: a))
            return
        }
        Task {
            switch await call("fix:\(check.id)", { $0.runFix(fix) }) {
            case .success:
                next.fixNotes[check.id] = "Done: \(fix.title.lowercased())"
                if fix == .daemonRestart { refresh(full: true) }
            case .failure(let e):
                next.fixErrors[check.id] = e
            }
            runDoctor()
        }
    }

    // MARK: F3 pin safety valve

    /// "Pin, even if it runs out": `account pin <id> --hard`.
    func pinHard(profile: String, account: String) {
        act("pin:\(profile)", title: "Could not pin \(profile)", profiles: true, card: profile,
            { $0.pin(account, hard: true) }) { [weak self] _ in
            self?.note = "\(profile) stays on \(account), even if it runs out"
        }
    }

    /// Whether the live pin of a card is a hard one; nil when unknown.
    func pinIsHard(_ card: ProfileCard) -> Bool? {
        card.pinned.flatMap { accountList?.account($0)?.pinHard }
    }

    func pinLiftNotice(_ card: ProfileCard) -> PinLift? {
        PinLift.notice(profile: card.name, lifts: next.pinLifts, pinned: card.isPinned,
                       dismissed: next.dismissedLifts, now: snapshot?.now ?? Date())
    }

    func dismissPinLift(_ l: PinLift) {
        next.dismissedLifts.insert(l.key)
        guard !preview else { return }
        UserDefaults.standard.set(Array(next.dismissedLifts.sorted().suffix(50)), forKey: "dismissedPinLifts")
    }

    // MARK: F4 folders

    func setFolders(_ profile: String, _ paths: [String], done: @escaping (CallError?) -> Void) {
        Task {
            let r = await call("folders:\(profile)", { $0.profilePaths(profile, paths) })
            done(r.failure)
            refresh(full: true, profiles: true)
        }
    }

    // MARK: after each refresh

    /// The audit log's unpins (F3) and the re-login reminders (F5), after a
    /// full refresh has read the account list.
    func refreshedNext() {
        guard !preview else { return }
        let path = paths.auditFile
        DispatchQueue.global(qos: .utility).async {
            let lifts = PinLift.readTail(path: path).map(PinLift.latest(jsonl:)) ?? [:]
            DispatchQueue.main.async { [weak self] in
                guard let self, self.next.pinLifts != lifts else { return }
                self.next.pinLifts = lifts
            }
        }
        ReloginNotifier.shared.check(accountList)
    }

    // MARK: opening Settings from outside a window

    /// Opens Settings on `pane` (and Add account, when set first).
    func openSettings(_ pane: Pane) {
        next.settingsRequest = SettingsRequest(pane: pane)
    }

    /// Signing an account in again, from a reminder or a Health fix.
    func beginSignIn(_ account: String) {
        addAccount = AddAccountRequest(account: account, profile: pool(of: account))
        openSettings(.accounts)
    }
}

/// F5: posts "personal needs signing in within 5 days" once per account per
/// day. The days sent are kept in UserDefaults. Its category and Sign in
/// action are registered, and its clicks routed, by the app's one
/// notification router (Notifier, AppFlows.swift; NoticeRouter).
@MainActor
final class ReloginNotifier {
    static let shared = ReloginNotifier()
    static let category = NoticeCategory.relogin

    private weak var store: Store?
    private var authorized: Bool?

    /// Notifications need a bundled app (UNUserNotificationCenter throws
    /// without one), so `swift run` posts none.
    private var available: Bool { Bundle.main.bundleIdentifier != nil && Bundle.main.bundlePath.hasSuffix(".app") }

    func install(_ store: Store) {
        self.store = store
    }

    func check(_ list: AccountList?) {
        guard available else { return }
        let defaults = UserDefaults.standard
        let sent = defaults.dictionary(forKey: ReloginReminders.defaultsKey) as? [String: String] ?? [:]
        let due = ReloginReminders.due(list, now: Date(), sent: sent)
        guard !due.isEmpty else { return }
        // Recorded before posting, so a refresh meanwhile never posts twice.
        defaults.set(ReloginReminders.record(sent, accounts: due.map(\.account), now: Date()),
                     forKey: ReloginReminders.defaultsKey)
        authorize { ok in
            guard ok else { return }
            let center = UNUserNotificationCenter.current()
            for r in due {
                let c = UNMutableNotificationContent()
                c.title = r.title
                c.body = r.body
                c.categoryIdentifier = Self.category
                center.add(UNNotificationRequest(identifier: r.identifier, content: c, trigger: nil))
            }
        }
    }

    private func authorize(_ then: @escaping @MainActor (Bool) -> Void) {
        if let a = authorized { then(a); return }
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { ok, _ in
            Task { @MainActor in
                self.authorized = ok
                then(ok)
            }
        }
    }
}

/// Opens the Settings window when the store asks (a notification's action
/// has no window to open it from). It lives in the menu-bar label, the one
/// view that is always there.
struct SettingsOpener: View {
    @ObservedObject var store: Store
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Color.clear.frame(width: 0, height: 0)
            .onChange(of: store.next.settingsRequest) { r in
                guard r != nil else { return }
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: SettingsView.windowID)
            }
    }
}
