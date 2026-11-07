import ClaudeSwitchCore
import SwiftUI

// Next refill (owner's picks, 2026-11-06; docs/IMPROVEMENTS.md): the popover's
// line and Accounts' Refills view. What they say comes from Refills.swift.

/// The open card's quiet line: the soonest refill among the profile's other
/// accounts. Hovering names the next four; a click opens the Refills view.
/// Nothing to come, no line.
struct RefillLine: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard
    let now: Date

    var body: some View {
        if let line = RefillText.nextLine(card, now: now) {
            Button { store.showRefills() } label: {
                Label(line, systemImage: "clock.arrow.circlepath")
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(RefillText.tooltip(card, now: now) ?? line)
            .accessibilityHint("Opens Settings → Accounts, Refills")
        }
    }
}

/// Accounts' Refills view: a lane per account and a dot per refill, sized by
/// what it frees, over 5 hours, a day or a week, with the refills in time
/// order under it.
struct RefillsTimelineView: View {
    @EnvironmentObject var store: Store
    @State var zoom = RefillZoom.week
    @State var atLeast = RefillAtLeast.any

    static let nameWidth: CGFloat = 112
    static let laneHeight: CGFloat = 22
    static let axisHeight: CGFloat = 16

    var body: some View {
        let now = store.now
        let t = RefillTimeline.build(store.snapshot.map(Refills.all) ?? [], zoom: zoom, atLeast: atLeast, now: now)
        VStack(alignment: .leading, spacing: 8) {
            controls.padding(.horizontal, 24)
            if let empty = t.emptyLine {
                VStack(spacing: 8) {
                    Text(empty).foregroundStyle(.secondary)
                    if t.canWiden {
                        Button("Show the Whole Week") {
                            zoom = .week
                            atLeast = .any
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 8) {
                        lanes(t, now: now)
                        rows(t, now: now)
                    }
                    .padding(.horizontal, 24).padding(.bottom, 24)
                }
            }
        }
    }

    var controls: some View {
        HStack(spacing: 8) {
            Picker("Show", selection: $zoom) {
                ForEach(RefillZoom.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()
            .help("How far ahead to look")
            Menu(atLeast.label) {
                ForEach(RefillAtLeast.allCases) { l in Button(l.label) { atLeast = l } }
            }
            .fixedSize()
            .help("Only refills that free at least this much")
            Spacer()
            HStack(spacing: 12) {
                legend(.csAccent, "Week")
                legend(.csSessionLine, "Session")
            }
            .font(.caption).foregroundStyle(.secondary)
        }
    }

    func legend(_ color: Color, _ title: String) -> some View {
        HStack(spacing: 4) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(title)
        }
    }

    /// The picture. VoiceOver reads the rows under it, which say the same.
    func lanes(_ t: RefillTimeline, now: Date) -> some View {
        VStack(spacing: 0) {
            ForEach(t.lanes, id: \.self) { name in
                HStack(spacing: 8) {
                    Text(name).font(.caption).lineLimit(1).truncationMode(.middle)
                        .frame(width: Self.nameWidth, alignment: .leading)
                        .help(name)
                    GeometryReader { g in
                        Rectangle().fill(Color.secondary.opacity(0.15)).frame(height: 1)
                            .position(x: g.size.width / 2, y: g.size.height / 2)
                            .frame(width: g.size.width)
                        ForEach(t.events.filter { $0.account == name }) { e in
                            mark(e, now: now, size: g.size)
                        }
                    }
                }
                .frame(height: Self.laneHeight)
            }
            HStack(spacing: 8) {
                Color.clear.frame(width: Self.nameWidth, height: 1)
                GeometryReader { g in
                    ForEach(zoom.ticks, id: \.label) { tick in
                        Text(tick.label).font(.caption2).foregroundStyle(.secondary).monospacedDigit().fixedSize()
                            .position(x: min(g.size.width - 8, g.size.width * tick.at), y: Self.axisHeight / 2)
                    }
                }
            }
            .frame(height: Self.axisHeight)
        }
        .background(alignment: .leading) {
            GeometryReader { g in
                let plot = g.size.width - Self.nameWidth - 8
                ForEach(zoom.ticks, id: \.label) { tick in
                    Rectangle().fill(Color.secondary.opacity(0.12))
                        .frame(width: 1, height: max(0, g.size.height - Self.axisHeight))
                        .offset(x: Self.nameWidth + 8 + plot * tick.at)
                }
            }
        }
        .accessibilityHidden(true)
    }

    /// A dot exactly at its time, its figure beside it; near the right end
    /// the figure goes on its left, so the dot is never moved to fit it.
    func mark(_ e: Refill, now: Date, size: CGSize) -> some View {
        let dot = 7 + CGFloat(e.frees / 100) * 9
        let x = size.width * CGFloat(zoom.position(e, now: now))
        let labelWidth: CGFloat = 34
        let left = x + dot / 2 + 3 + labelWidth > size.width
        return ZStack {
            Circle().fill(e.isWeek ? Color.csAccent : Color.csSessionLine)
                .frame(width: dot, height: dot)
                .position(x: x, y: size.height / 2)
            Text(Format.pct(e.frees)).font(.caption2).foregroundStyle(.secondary).monospacedDigit()
                .frame(width: labelWidth, alignment: left ? .trailing : .leading)
                .position(x: left ? x - dot / 2 - 3 - labelWidth / 2 : x + dot / 2 + 3 + labelWidth / 2,
                          y: size.height / 2)
        }
        .help("\(e.account): \(RefillText.frees(e)) \(RefillText.countdown(e, now: now)) (\(Runway.when(e.at, now: now)))")
    }

    func rows(_ t: RefillTimeline, now: Date) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(t.events) { e in
                HStack(spacing: 12) {
                    Text(RefillText.countdown(e, now: now)).monospacedDigit().frame(width: 84, alignment: .leading)
                    Text(e.account).fontWeight(.medium).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Text(RefillText.frees(e)).foregroundStyle(.secondary).fixedSize()
                }
                .padding(.vertical, 4)
                .help(Runway.when(e.at, now: now))
                .accessibilityElement(children: .combine)
                if e.id != t.events.last?.id { Divider() }
            }
        }
    }
}
