import Foundation
import Testing
@testable import ClaudeSwitchCore

/// F11: the app's actionable notifications. The daemon notifies through
/// osascript, which cannot carry actions, so the app posts its own when it
/// sees a change in state.json: a rotation (Undo, Pin here) and an account
/// that needs a sign-in (Sign in).
@Suite struct NoticeTests {
    let t0 = Date(timeIntervalSince1970: 1_800_000_000)

    func state(_ active: String?, _ status: [String: NoticeStatus] = [:], profile: String = "work") -> NoticeState {
        NoticeState(profiles: [NoticeProfile(name: profile, active: active, status: status)])
    }

    @Test func testARotationCarriesUndoAndPinHere() throws {
        let n = Notices.diff(old: state("work-1"), new: state("work-2"), appSwitches: AppSwitches(), now: t0)
        #expect(n.count == 1)
        let r = try #require(n.first)
        #expect(r.category == NoticeCategory.rotation)
        #expect(r.title == "work → work-2")
        #expect(r.body.contains("work-1"))
        #expect(Notices.command(action: NoticeAction.undo, info: r.info) == .use(account: "work-1", profile: "work"))
        #expect(Notices.command(action: NoticeAction.pinHere, info: r.info) == .pin(account: "work-2"))
        #expect(Notices.command(action: NoticeAction.signIn, info: r.info) == nil, "not an action of this category")
    }

    @Test func testNothingOnTheFirstReadOrWithoutAChange() {
        #expect(Notices.diff(old: nil, new: state("work-2"), appSwitches: AppSwitches(), now: t0).isEmpty)
        #expect(Notices.diff(old: state("work-2"), new: state("work-2"), appSwitches: AppSwitches(), now: t0).isEmpty)
        #expect(Notices.diff(old: state(nil), new: state("work-2"), appSwitches: AppSwitches(), now: t0).isEmpty,
                "a profile signing in for the first time is not a rotation")
        #expect(Notices.diff(old: state("work-2"), new: state(nil), appSwitches: AppSwitches(), now: t0).isEmpty)
    }

    @Test func testASwitchMadeInTheAppIsNotAnnounced() {
        var mine = AppSwitches()
        mine.record(profile: "work", to: "work-2", at: t0)
        #expect(Notices.diff(old: state("work-1"), new: state("work-2"), appSwitches: mine, now: t0.addingTimeInterval(30)).isEmpty)
        #expect(Notices.diff(old: state("work-1"), new: state("work-2"), appSwitches: mine,
                             now: t0.addingTimeInterval(AppSwitches.window + 1)).count == 1,
                "long after, the same switch is the daemon's")
        #expect(Notices.diff(old: state("work-1"), new: state("work-3"), appSwitches: mine, now: t0).count == 1,
                "another target is not the app's switch")
    }

    @Test func testTheLiveAccountNeedingASignInCarriesSignIn() throws {
        let n = Notices.diff(old: state("work-1", ["work-1": .ok]), new: state("work-1", ["work-1": .needsLogin]),
                             appSwitches: AppSwitches(), now: t0)
        let s = try #require(n.first)
        #expect(n.count == 1)
        #expect(s.category == NoticeCategory.signIn)
        #expect(s.title == "work-1 needs a sign-in")
        #expect(Notices.command(action: NoticeAction.signIn, info: s.info) == .signIn(account: "work-1", profile: "work"))
        #expect(Notices.command(action: NoticeAction.undo, info: s.info) == nil)
    }

    @Test func testARefusalOfTheLiveAccountCarriesSignIn() throws {
        let n = Notices.diff(old: state("work-1", ["work-1": .ok]), new: state("work-1", ["work-1": .refused]),
                             appSwitches: AppSwitches(), now: t0)
        let s = try #require(n.first)
        #expect(s.category == NoticeCategory.signIn)
        #expect(s.title == "work-1 was refused")
    }

    @Test func testOnlyTransitionsAreAnnounced() {
        let same = Notices.diff(old: state("work-1", ["work-1": .needsLogin]), new: state("work-1", ["work-1": .needsLogin]),
                                appSwitches: AppSwitches(), now: t0)
        #expect(same.isEmpty, "still needing a sign-in is not news")
        let idleRefused = Notices.diff(old: state("work-1", ["work-2": .ok]), new: state("work-1", ["work-2": .refused]),
                                       appSwitches: AppSwitches(), now: t0)
        #expect(idleRefused.isEmpty, "an idle account's refusal is rotation's business")
        let idleLogin = Notices.diff(old: state("work-1", ["work-2": .ok]), new: state("work-1", ["work-2": .needsLogin]),
                                     appSwitches: AppSwitches(), now: t0)
        #expect(idleLogin.count == 1, "an idle account that can no longer sign in is")
        let firstSeen = Notices.diff(old: state("work-1", [:]), new: state("work-1", ["work-2": .needsLogin]),
                                     appSwitches: AppSwitches(), now: t0)
        #expect(firstSeen.isEmpty, "an account not seen before has no transition")
    }

    @Test func testCommandsRefuseIdsThatAreNotNames() {
        let bad: [String: String] = ["kind": "rotation", "profile": "work", "from": "--force", "to": "work-2"]
        #expect(Notices.command(action: NoticeAction.undo, info: bad) == nil)
        #expect(Notices.command(action: "cs.unknown", info: ["kind": "rotation", "profile": "work",
                                                             "from": "work-1", "to": "work-2"]) == nil)
    }

    @Test func testTheStateComesFromTheSnapshot() throws {
        let s = NoticeState(try fixtureSnapshot())
        #expect(!s.profiles.isEmpty)
        for p in s.profiles {
            if let a = p.active { #expect(p.status[a] != nil, "the live account's status is known") }
        }
    }

    @Test func testCategoriesHaveTheirActions() {
        #expect(NoticeCategory.actions(NoticeCategory.rotation).map(\.id) == [NoticeAction.undo, NoticeAction.pinHere])
        #expect(NoticeCategory.actions(NoticeCategory.rotation).map(\.title) == ["Undo", "Pin here"])
        #expect(NoticeCategory.actions(NoticeCategory.signIn).map(\.title) == ["Sign in"])
    }
}

/// One notification router for the whole app: F11's rotation and sign-in
/// categories and F5's re-login reminder share one delegate, one category
/// registration and one dispatch (UNUserNotificationCenter has a single
/// delegate).
@Suite struct NoticeRouterTests {
    @Test func testEveryCategoryIsRegisteredWithItsActions() {
        #expect(NoticeCategory.all == [NoticeCategory.rotation, NoticeCategory.signIn, NoticeCategory.relogin])
        #expect(NoticeCategory.relogin == "xyz.claudeswitch.relogin", "F5's identifier, unchanged")
        #expect(NoticeCategory.actions(NoticeCategory.relogin).map(\.id) == [NoticeAction.reloginSignIn])
        #expect(NoticeCategory.actions(NoticeCategory.relogin).map(\.title) == ["Sign in…"])
        for c in NoticeCategory.all { #expect(!NoticeCategory.actions(c).isEmpty, "\(c) has its actions") }
    }

    @Test func testAReloginReminderRoutesToSignIn() {
        let id = ReloginReminder(account: "personal", expiresAt: Date(), days: 3).identifier
        for action in [NoticeAction.reloginSignIn, NoticeAction.defaultTap] {
            #expect(NoticeRouter.command(category: NoticeCategory.relogin, action: action, identifier: id, userInfo: [:])
                    == .signIn(account: "personal", profile: nil), "\(action)")
        }
        #expect(NoticeRouter.command(category: NoticeCategory.relogin, action: NoticeAction.undo, identifier: id,
                                     userInfo: [:]) == nil)
        #expect(NoticeRouter.command(category: NoticeCategory.relogin, action: NoticeAction.reloginSignIn,
                                     identifier: "xyz.claudeswitch.relogin.--x", userInfo: [:]) == nil)
    }

    @Test func testRotationAndSignInRouteThroughTheSameRouter() {
        let rot: [AnyHashable: Any] = ["kind": "rotation", "profile": "work", "from": "work-1", "to": "work-2"]
        #expect(NoticeRouter.command(category: NoticeCategory.rotation, action: NoticeAction.undo, identifier: "r",
                                     userInfo: rot) == .use(account: "work-1", profile: "work"))
        #expect(NoticeRouter.command(category: NoticeCategory.rotation, action: NoticeAction.pinHere, identifier: "r",
                                     userInfo: rot) == .pin(account: "work-2"))
        #expect(NoticeRouter.command(category: NoticeCategory.rotation, action: NoticeAction.defaultTap, identifier: "r",
                                     userInfo: rot) == nil, "a tap on a rotation does nothing")
        let si: [AnyHashable: Any] = ["kind": "signin", "profile": "work", "account": "work-1"]
        for action in [NoticeAction.signIn, NoticeAction.defaultTap] {
            #expect(NoticeRouter.command(category: NoticeCategory.signIn, action: action, identifier: "s", userInfo: si)
                    == .signIn(account: "work-1", profile: "work"))
        }
        #expect(NoticeRouter.command(category: "other", action: NoticeAction.signIn, identifier: "s", userInfo: si) == nil)
    }
}
