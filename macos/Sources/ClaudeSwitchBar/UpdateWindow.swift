import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The update window (0.8.9), its own NSWindow like the setup one, so the
/// ⋯ menu, Check now and the popover's Update… can open it from anywhere.
@MainActor
enum UpdateWindow {
    static let title = "Software Update"
    private static var window: NSWindow?
    private static var closeObserver: NSObjectProtocol?
    private static var sizeObservers: [NSObjectProtocol] = []

    static func show(_ updates: UpdateController) {
        WindowFocus.closePopover()
        NSApp.activate(ignoringOtherApps: true)
        if let w = window {
            WindowFocus.bringForward { w }
            return
        }
        let host = NSHostingController(rootView: UpdateDialogView(updates: updates))
        host.sizingOptions = [.preferredContentSize]
        let w = NSWindow(contentViewController: host)
        w.title = title
        w.styleMask = [.titled, .closable]
        w.isReleasedWhenClosed = false
        w.setContentSize(host.view.fittingSize)
        WindowFocus.centre(w, on: WindowFocus.pointerScreen)
        sizeObservers = WindowFocus.keepCentred(w)
        window = w
        closeObserver = NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w,
                                                               queue: .main) { _ in
            MainActor.assumeIsolated {
                if let o = closeObserver { NotificationCenter.default.removeObserver(o) }
                sizeObservers.forEach(NotificationCenter.default.removeObserver)
                sizeObservers = []
                closeObserver = nil
                window = nil
                AppHub.updates.windowClosed()
            }
        }
        WindowFocus.bringForward { w }
    }

    static func close() { window?.close() }
}

/// The update's progress bar, drawn rather than the system's: in this
/// window and the popover's foot the system bar came out grey, and in dark
/// its track, (28,28,28) on (30,30,30), all but vanished (visual QA
/// 2026-10-28). The accent on a track that shows in both themes.
struct UpdateBar: View {
    var value: Double
    var height: CGFloat = 6

    var body: some View {
        let v = min(1, max(0, value))
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.15))
                Capsule()
                    .fill(Color.csAccent)
                    .frame(width: v > 0 ? max(height, g.size.width * v) : 0)
            }
        }
        .frame(height: height)
        .animation(.easeOut(duration: 0.2), value: v)
        .accessibilityElement()
        .accessibilityValue("\(Int((v * 100).rounded())) percent")
    }
}

/// What the window draws for each state: the app's icon, a title, a line
/// that says what is going on, then what the state has (what is new, the
/// progress bar) and its buttons, the default one last.
struct UpdateDialogView: View {
    @ObservedObject var updates: UpdateController

    static let icon: NSImage? = AppIcon.png(pixels: 128).flatMap(NSImage.init(data:))

    var body: some View {
        let d = updates.dialog ?? .checking
        HStack(alignment: .top, spacing: 16) {
            Group {
                if let icon = Self.icon { Image(nsImage: icon).resizable() } else { Color.clear }
            }
            .frame(width: 64, height: 64)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 10) {
                Text(d.title).font(.system(size: 15, weight: .semibold))
                messageLine(d)
                content(d)
                buttons(d).padding(.top, 4)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(20)
        .frame(width: 500)
    }

    /// The line under the title. While checking, a spinner beside it (a bar
    /// with no value read as stuck at 0%), not alone on a line of its own.
    @ViewBuilder func messageLine(_ d: UpdateDialog) -> some View {
        let text = Text(d.message)
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .textSelection(.enabled)
        if d == .checking {
            HStack(spacing: 8) {
                ProgressView().progressViewStyle(.circular).controlSize(.small)
                text
            }
        } else {
            text
        }
    }

    @ViewBuilder func content(_ d: UpdateDialog) -> some View {
        switch d {
        case .available(_, _, let notes, _, _):
            if let notes {
                VStack(alignment: .leading, spacing: 4) {
                    Text("What’s new").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ScrollView {
                        VStack(alignment: .leading, spacing: 2) {
                            ForEach(Array(UpdateNotes.lines(notes).enumerated()), id: \.offset) { _, line in
                                note(line)
                            }
                        }
                        .font(.callout)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textSelection(.enabled)
                        .padding(10)
                    }
                    .frame(maxHeight: 180)
                    .fixedSize(horizontal: false, vertical: true)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color(nsColor: .separatorColor)))
                }
            }
            if let detail = d.detail {
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
        case .installing(_, let step):
            HStack(spacing: 10) {
                UpdateBar(value: step.fraction)
                // Only while there is a figure: an empty column kept the
                // bar 34 points short of the edge in the quick steps.
                if let percent = step.percent {
                    Text(percent)
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .frame(width: 34, alignment: .trailing)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityLabel(step.label)
            if let note = step.note {
                Text(note).font(.caption).foregroundStyle(.secondary)
            }
        default:
            EmptyView()
        }
    }

    @ViewBuilder func buttons(_ d: UpdateDialog) -> some View {
        HStack(spacing: 10) {
            switch d {
            case .checking:
                Spacer()
                Button("Cancel") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
            case .checkOff:
                Spacer()
                Button("Close") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
                primary("Turn On") { updates.turnOn() }
            case .upToDate, .versionUnknown, .installed:
                Spacer()
                primary("OK") { updates.closeDialog() }
                    .background { escape { updates.closeDialog() } }
            case .checkFailed:
                Spacer()
                Button("OK") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
                primary("Try Again") { updates.checkNow() }
            case .available:
                pageLink(d)
                Spacer()
                Button("Later") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
                primary("Install Update") { updates.update() }
            case .installing(_, let step):
                Spacer()
                Button("Cancel") { updates.cancelInstall() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(!step.cancellable)
                    .help(step.cancellable ? "Stops before anything is replaced."
                          : "Too late to stop: the new files are going in.")
            case .failed:
                pageLink(d)
                Spacer()
                Button("Close") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
                primary("Try Again") {
                    updates.dismissFailure()
                    updates.checkNow()
                }
            }
        }
    }

    func primary(_ title: String, _ action: @escaping () -> Void) -> some View {
        Button(title, action: action)
            .buttonStyle(PrimaryButtonStyle())
            .fixedSize()
            .keyboardShortcut(.defaultAction)
    }

    /// Release Notes on GitHub on the offer; Release page on GitHub after a
    /// failure, where the release can be downloaded by hand.
    @ViewBuilder func pageLink(_ d: UpdateDialog) -> some View {
        if let page = d.page, let label = d.pageLabel {
            Button(label) { NSWorkspace.shared.open(page) }
                .buttonStyle(.link)
                .font(.callout)
        }
    }

    /// Escape, where the only button is the default one: a button takes one
    /// shortcut, so OK keeps Return and an unseen one takes Escape (as the
    /// popover's ⌘R and ⌘Q do).
    func escape(_ action: @escaping () -> Void) -> some View {
        Button("Close", action: action)
            .keyboardShortcut(.cancelAction)
            .opacity(0)
            .frame(width: 0, height: 0)
            .accessibilityHidden(true)
    }

    /// A line of What's new; a bullet's wrapped lines hang under its text.
    @ViewBuilder func note(_ line: UpdateNoteLine) -> some View {
        if line.bullet {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text("•")
                Text(Self.markdown(line.text)).fixedSize(horizontal: false, vertical: true)
            }
        } else {
            Text(Self.markdown(line.text.isEmpty ? " " : line.text)).fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Inline Markdown, keeping the release's line breaks.
    static func markdown(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(s)
    }
}
