import AppKit
import Charts
import ClaudeSwitchCore
import SwiftUI

// The views of F1-F4, F7 and F12 (docs/IMPROVEMENTS.md, "Next features").
// Each shows only what the CLI reported: a key an older binary lacks hides
// its row, and an unknown figure is never drawn as 0.

// MARK: F2 the pool line

/// "work pool: runs dry Thu 14:00 at this pace"; amber within two hours.
/// Hidden when the CLI does not know (null) or does not say.
struct PoolLine: View {
    let card: ProfileCard
    let now: Date

    var body: some View {
        if let line = Runway.poolLine(profile: card.name, dryAt: card.poolDryAt, now: now) {
            let urgent = Runway.urgent(dryAt: card.poolDryAt, now: now)
            Label(line, systemImage: "hourglass")
                .font(.caption)
                .foregroundStyle(urgent ? Color.orange : Color.secondary)
                .lineLimit(1).truncationMode(.tail)
                .help(line)
        }
    }
}

// MARK: F3 the pin safety valve

/// The daemon lifted this card's pin: why, until dismissed or a day old.
struct PinLiftBanner: View {
    @EnvironmentObject var store: Store
    let lift: PinLift

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "pin.slash").foregroundStyle(.orange)
            Text(lift.message + ", so rotation runs again.")
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 4)
            Button { store.dismissPinLift(lift) } label: { Image(systemName: "xmark") }
                .buttonStyle(.borderless).font(.caption)
                .accessibilityLabel("Dismiss")
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.orange.opacity(0.12)))
        .accessibilityElement(children: .combine)
    }
}

/// Pin's ⋯: "Pin, even if it runs out" (`account pin --hard`), offered only
/// by a CLI that reports pin_hard.
struct HardPinMenu: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        if !card.isPinned, let a = card.active, store.accountList?.knowsHardPin == true {
            Menu {
                Button("Pin, even if it runs out") { store.pinHard(profile: card.name, account: a.id) }
            } label: {
                Image(systemName: "ellipsis")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .disabled(store.isBusy("pin:\(card.name)"))
            .help("More ways to pin \(card.name)")
            .accessibilityLabel("More ways to pin \(card.name)")
        }
    }
}

// MARK: F4 folders

/// Settings → Profiles: the folders that pick this profile (`cs run` with no
/// name, `profile which`). Hidden when the CLI does not report paths.
struct FoldersField: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    let paths: [String]
    @State private var text = ""
    @State private var edited = false
    @State private var error: CallError?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("FOLDERS").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 4) {
                TextField("Folders", text: Binding(get: { text }, set: { text = $0; edited = true; error = nil }),
                          prompt: Text(verbatim: "~/work/**, ~/clients/**"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .onSubmit(save)
                    .accessibilityLabel("Folders that use \(profile.name)")
                if store.isBusy("folders:\(profile.name)") { ProgressView().controlSize(.mini) }
            }
            if let e = FolderList.validate(text) {
                InlineError(message: "Must be folders: " + e, hint: "")
            } else if let e = error {
                InlineError(message: e.message, hint: e.hint)
            } else {
                Text(paths.isEmpty
                     ? "No folders: claudeswitch run picks \(profile.name) only by name."
                     : "claudeswitch run in these folders uses \(profile.name). Comma-separated; ** matches any depth.")
                    .font(.caption2).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .onAppear { text = FolderList.text(paths) }
        .onChange(of: paths) { p in if !edited { text = FolderList.text(p) } }
    }

    func save() {
        let new = FolderList.parse(text)
        guard FolderList.validate(text) == nil, new != paths else { return }
        store.setFolders(profile.name, new) { e in
            error = e
            if e == nil { edited = false }
        }
    }
}

// MARK: F1 an enum override (Spend first, per profile)

/// A per-profile choice for an enum setting: inherit, or one of its values.
struct EnumOverrideField: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    let setting: SettingSchema
    @State private var error: AppError?

    var global: String { store.values?.values[setting.key] ?? setting.defaultValue }
    var label: String { SettingNames.label(setting.key) }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.caption).lineLimit(1)
            HStack(spacing: 4) {
                Picker(label, selection: Binding(get: { profile.overrides[setting.key] ?? "" }, set: save)) {
                    ForEach(OverrideChoices.options(setting, global: global), id: \.value) { Text($0.label).tag($0.value) }
                }
                .labelsHidden()
                .accessibilityLabel("\(label) in \(profile.name)")
                if store.isBusy("override:\(profile.name):\(setting.key)") { ProgressView().controlSize(.mini) }
            }
            if let e = error {
                InlineError(message: e.message, hint: e.hint)
            } else {
                Text(profile.overrides[setting.key] == nil ? "inherits \(SettingNames.value(setting, global))"
                     : "overrides \(SettingNames.value(setting, global))")
                    .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }

    func save(_ v: String) {
        guard v != (profile.overrides[setting.key] ?? "") else { return }
        let (p, k) = (profile.name, setting.key)
        error = nil
        Task {
            switch await store.call("override:\(p):\(k)", { $0.profileSet(p, k, v) }) {
            case .success: store.refresh(full: true, profiles: true)
            case .failure(.app(let e)): error = e
            case .failure(let e): error = AppError(code: "failed", message: e.message, hint: e.hint)
            }
        }
    }
}

// MARK: F7 Settings → History

struct HistoryPane: View {
    @EnvironmentObject var store: Store

    var now: Date { store.snapshot?.now ?? Date() }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            PaneHeader(title: "History",
                       subtitle: "Each account's session and week over the last 30 days, with its switches.") {
                Button { store.loadHistory() } label: { Label("Refresh", systemImage: "arrow.clockwise") }
                    .disabled(store.binaryPath == nil || store.isBusy("load:history"))
            }
            .padding([.horizontal, .top], 24)
            .padding(.bottom, 12)
            content
        }
        .onAppear { if store.next.history == nil { store.loadHistory() } }
    }

    @ViewBuilder var content: some View {
        if let h = store.next.history {
            let ids = HistoryChart.accounts(h, now: now)
            if ids.isEmpty {
                Text("No readings in the last 30 days yet: the daemon records them as it polls.")
                    .foregroundStyle(.secondary).padding(.horizontal, 24)
            } else {
                HistoryLegend().padding(.horizontal, 24).padding(.bottom, 8)
                ScrollView {
                    VStack(spacing: 12) {
                        ForEach(ids, id: \.self) { id in
                            AccountHistoryChart(history: h, account: id, now: now,
                                                plan: store.accountList?.account(id)?.plan)
                        }
                    }
                    .padding(.horizontal, 24).padding(.bottom, 24)
                }
            }
        } else if let e = store.next.historyError {
            PaneUnavailable(error: e, what: "Usage history").padding(.horizontal, 24)
        } else {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Reading the history…").foregroundStyle(.secondary)
            }
            .padding(.horizontal, 24)
        }
    }
}

/// What each mark means: the week in the accent and the session in the
/// second accent (the rings' outer and inner), switches as rules.
struct HistoryLegend: View {
    var body: some View {
        HStack(spacing: 16) {
            item(Rectangle().fill(Color.csAccent).frame(width: 14, height: 3), "Week")
            item(Rectangle().fill(Color.csAccent2).frame(width: 14, height: 2), "Session")
            item(Rectangle().fill(Color.secondary).frame(width: 1.5, height: 12), "Switched to it")
            item(DashedRule().stroke(Color.secondary, style: StrokeStyle(lineWidth: 1.5, dash: [3, 2]))
                    .frame(width: 2, height: 12), "Switched away")
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    func item<V: View>(_ mark: V, _ label: String) -> some View {
        HStack(spacing: 6) { mark; Text(label) }
    }
}

private struct DashedRule: Shape {
    func path(in r: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: r.midX, y: r.minY))
        p.addLine(to: CGPoint(x: r.midX, y: r.maxY))
        return p
    }
}

/// One account's 30 days: the week and the session (each line broken
/// where readings stopped), and the switches to and away from it.
struct AccountHistoryChart: View {
    let history: UsageHistory
    let account: String
    let now: Date
    let plan: String?

    var body: some View {
        let week = HistoryChart.points(history, account: account, window: .week, now: now)
        let session = HistoryChart.points(history, account: account, window: .session, now: now)
        let marks = HistoryChart.switches(history, account: account, now: now)
        let domain = HistoryChart.domain(now: now)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(account).font(.headline)
                if let p = plan { Text(p).font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Text(summary(week: week, marks: marks)).font(.caption).foregroundStyle(.secondary).monospacedDigit()
            }
            Chart {
                ForEach(marks) { m in
                    RuleMark(x: .value("Switched", m.at))
                        .foregroundStyle(Color.secondary.opacity(0.7))
                        .lineStyle(StrokeStyle(lineWidth: 1, dash: m.into ? [] : [3, 2]))
                }
                ForEach(session) { p in
                    LineMark(x: .value("Time", p.at), y: .value("Used", p.value),
                             series: .value("Line", "session-\(p.segment)"))
                        .foregroundStyle(Color.csAccent2)
                        .lineStyle(StrokeStyle(lineWidth: 1))
                        .interpolationMethod(.linear)
                }
                ForEach(week) { p in
                    LineMark(x: .value("Time", p.at), y: .value("Used", p.value),
                             series: .value("Line", "week-\(p.segment)"))
                        .foregroundStyle(Color.csAccent)
                        .lineStyle(StrokeStyle(lineWidth: 2))
                        .interpolationMethod(.linear)
                }
            }
            .chartXScale(domain: domain)
            .chartYScale(domain: 0...100)
            .chartYAxis {
                AxisMarks(position: .leading, values: [0, 50, 100]) { v in
                    AxisGridLine()
                    AxisValueLabel { Text("\(v.as(Int.self) ?? 0)%") }
                }
            }
            .chartXAxis {
                // No vertical grid: the only vertical lines are switches.
                AxisMarks(values: .stride(by: .day, count: 7)) { _ in
                    AxisTick()
                    AxisValueLabel(format: .dateTime.day().month(.abbreviated))
                }
            }
            .frame(height: 120)
            .accessibilityLabel("\(account): \(summary(week: week, marks: marks))")
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.2)))
    }

    /// "week now 41% · 4 switches".
    func summary(week: [ChartPoint], marks: [SwitchMark]) -> String {
        var parts: [String] = []
        if let last = week.last { parts.append("week \(Format.pct(last.value))") }
        parts.append("\(marks.count) switch\(marks.count == 1 ? "" : "es")")
        return parts.joined(separator: " · ")
    }
}

/// A pane whose CLI command failed: a newer claudeswitch is needed, or the
/// CLI's own error.
struct PaneUnavailable: View {
    let error: CallError
    let what: String

    var body: some View {
        if FeatureSupport.needsNewerCLI(error) {
            Label(FeatureSupport.newerCLI(what), systemImage: "arrow.down.circle")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else {
            InlineError(message: error.message, hint: error.hint)
        }
    }
}

// MARK: F12 Settings → Health

struct HealthPane: View {
    @EnvironmentObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            PaneHeader(title: "Health", subtitle: "What claudeswitch doctor checks, with a fix for what fails.") {
                Button { store.runDoctor() } label: {
                    if store.isBusy("doctor") {
                        ProgressView().controlSize(.small)
                    } else {
                        Label("Run again", systemImage: "arrow.clockwise")
                    }
                }
                .disabled(store.binaryPath == nil || store.isBusy("doctor"))
            }
            .padding([.horizontal, .top], 24)
            .padding(.bottom, 12)
            content
        }
        .onAppear { if store.next.doctor == nil { store.runDoctor() } }
    }

    @ViewBuilder var content: some View {
        if let d = store.next.doctor {
            Text(d.summary + (store.next.doctorAt.map { " · checked " + Format.age($0, now: Date()) } ?? ""))
                .font(.callout).foregroundStyle(.secondary)
                .padding(.horizontal, 24).padding(.bottom, 8)
            if let e = store.next.doctorError {
                InlineError(message: "Could not run the checks again: " + e.message, hint: e.hint)
                    .padding(.horizontal, 24).padding(.bottom, 8)
            }
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(d.checks) { c in
                        CheckRow(check: c)
                        if c.id != d.checks.last?.id { Divider() }
                    }
                }
                .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
                .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.2)))
                .padding(.horizontal, 24).padding(.bottom, 24)
            }
        } else if let e = store.next.doctorError {
            PaneUnavailable(error: e, what: "The health checks").padding(.horizontal, 24)
        } else {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Running the checks…").foregroundStyle(.secondary)
            }
            .padding(.horizontal, 24)
        }
    }
}

/// One check: its mark, name and message, and a Fix button for a failure
/// whose fix this app knows.
struct CheckRow: View {
    @EnvironmentObject var store: Store
    let check: DoctorCheck

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            mark.frame(width: 18)
            VStack(alignment: .leading, spacing: 2) {
                Text(check.name).fontWeight(.semibold)
                Text(check.message).font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let n = store.next.fixNotes[check.id] {
                    Text(n).font(.caption).foregroundStyle(Color.csAccent2)
                }
                if let e = store.next.fixErrors[check.id] {
                    if FeatureSupport.needsNewerCLI(e) {
                        Text("This fix needs a newer claudeswitch.").font(.caption).foregroundStyle(.secondary)
                    } else {
                        InlineError(message: e.message, hint: e.hint)
                    }
                }
            }
            Spacer(minLength: 8)
            if check.offersFix, let fix = check.fix {
                Button {
                    store.runFix(check)
                } label: {
                    if store.isBusy("fix:\(check.id)") {
                        ProgressView().controlSize(.small)
                    } else {
                        Text(fix.title)
                    }
                }
                .disabled(store.binaryPath == nil || store.isBusy("fix:\(check.id)"))
                .help(check.fixText.map { "Runs: claudeswitch \($0)" } ?? fix.title)
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder var mark: some View {
        switch check.status {
        case .ok:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(Color.csAccent2).accessibilityLabel("ok")
        case .warn:
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange).accessibilityLabel("warning")
        case .fail:
            Image(systemName: "xmark.octagon.fill").foregroundStyle(.red).accessibilityLabel("failing")
        case .unknown:
            Image(systemName: "questionmark.circle").foregroundStyle(.secondary).accessibilityLabel("unknown")
        }
    }
}
