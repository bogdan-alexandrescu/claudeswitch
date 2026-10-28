import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Settings → Overview (owner decision 2026-10-26): the window's first page.
/// What needs the person, a tile per area that opens its pane, and the last
/// few things that happened. The figures come from ClaudeSwitchCore's
/// Overview; this only draws them.
struct OverviewPane: View {
    @EnvironmentObject var store: Store
    @ObservedObject var updates = AppHub.updates
    /// Opens a pane of this window.
    var go: (Pane) -> Void = { _ in }

    var model: Overview { Overview.build(input) }

    var input: OverviewInput {
        let s = store.snapshot
        let cards = s?.cards ?? []
        let values = store.values?.values ?? [:]
        let last = store.next.cleanRuns?.first
        let clean = values["clean"].map { on in
            OverviewClean(on: on == "true", every: values["clean_every"].flatMap(GoDuration.parse),
                          lastAt: last?.at, lastBytes: last?.bytes ?? 0, lastRemoved: last?.removed ?? 0,
                          lastPutBack: last?.undone ?? 0)
        }
        let switches = (store.next.history?.switches ?? []).suffix(6).map {
            OverviewSwitch(at: $0.at, profile: cards.count > 1 ? $0.profile : nil, from: $0.from, to: $0.to,
                           reason: $0.reason)
        }
        var i = OverviewInput(daemon: s?.daemon, cards: cards, accounts: s?.accounts ?? [],
                             pools: store.data.pools?.pools.count,
                             chrome: cards.compactMap { store.chromeNotice(for: $0)?.text }, clean: clean,
                             update: OverviewUpdate(current: updates.current, available: updates.available,
                                                    lastCheck: updates.lastCheck, enabled: updates.enabled,
                                                    failure: updates.lastFailure),
                             switches: Array(switches), now: store.now)
        i.chromeNotices = cards.compactMap { store.chromeNotice(for: $0) }
        i.healthFailing = store.next.doctor.map { $0.count(.fail) }
        i.healthAt = store.next.doctorAt
        // Only what keeps the app from acting at all; a failed read shows
        // where it happened.
        switch store.problem {
        case .missing?, .tooOld?, .unsupportedContract?: i.problem = store.problem?.message
        default: break
        }
        i.daemonMode = store.daemon?.mode
        i.settingsUnreadable = store.values == nil && store.valuesError != nil
        i.daemonInstalled = store.daemonMissing ? false : store.daemon?.installed
        return i
    }

    var body: some View {
        let o = model
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                PaneHeader(title: "Overview", subtitle: "What needs you, and how everything stands.")
                if !o.needs.isEmpty { needs(o.needs) }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 200), spacing: 12)], spacing: 12) {
                    ForEach(o.tiles) { tile($0) }
                }
                if !o.recent.isEmpty { recent(o.recent) }
            }
            .padding(24)
        }
        .onAppear {
            store.loadHistoryIfStale()
            store.loadCleanRuns()
        }
    }

    func needs(_ list: [OverviewNeed]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("NEEDS YOU").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            ForEach(list) { n in
                HStack(spacing: 10) {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(n.text).lineLimit(2).fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 8)
                    Button(n.action) { open(n.target, account: n.account) }.controlSize(.small)
                        .disabled(n.account.map { store.isBusy("chrome:\($0)") } ?? false)
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.orange.opacity(0.10)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.orange.opacity(0.35)))
    }

    func tile(_ t: OverviewTile) -> some View {
        Button { open(t.target) } label: {
            VStack(alignment: .leading, spacing: 6) {
                // A fixed box for the symbol, so every tile's title starts
                // at the same place whatever the symbol's width or height.
                // One row, centred, whatever the symbol: a Label set titles up
                // to a point apart.
                HStack(alignment: .center, spacing: 6) {
                    Image(systemName: t.symbol).frame(width: 18, height: 14)
                    Text(t.title)
                }
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Text(t.primary)
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(t.attention ? Color.orange : Color.primary)
                    .lineLimit(1)
                Text(t.secondary.isEmpty ? " " : t.secondary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
            }
            .padding(14)
            .frame(maxWidth: .infinity, minHeight: 104, alignment: .topLeading)
            .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
            .overlay(RoundedRectangle(cornerRadius: 10)
                .stroke(t.attention ? Color.orange.opacity(0.5) : Color(nsColor: .separatorColor)))
            .contentShape(RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
        .help(t.target == .update ? (updates.enabled ? "Check for updates" : "Open Advanced: checking is off")
              : "Open \(t.title)")
        .accessibilityElement(children: .combine)
    }

    func recent(_ events: [OverviewEvent]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("RECENT").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            ForEach(events) { e in
                HStack(alignment: .firstTextBaseline, spacing: 12) {
                    Text(e.when).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                        .frame(width: 52, alignment: .leading)
                    Text(e.text).font(.callout).lineLimit(1).truncationMode(.tail)
                }
            }
        }
    }

    func open(_ target: OverviewTarget, account: String? = nil) {
        switch target {
        case .signIn: if let a = account { store.beginSignIn(a) } else { go(.accounts) }
        case .chromeOwn: if let a = account { store.setAccountChrome(a, .createNew) } else { go(.accounts) }
        case .health: go(.health)
        case .advanced: go(.advanced)
        case .daemon: go(.daemon)
        case .profiles: go(.profiles)
        case .accounts: go(.accounts)
        case .rotation: go(.rotation)
        case .cleanup: go(.cleanup)
        case .update:
            switch input.update.action {
            case .advanced: go(.advanced)
            case .offer: updates.showOffer()
            case .check: updates.checkNow()
            }
        }
    }
}
