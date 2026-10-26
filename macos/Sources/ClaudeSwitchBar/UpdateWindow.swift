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
                Text(d.message)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                content(d)
                buttons(d).padding(.top, 4)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(20)
        .frame(width: 500)
    }

    @ViewBuilder func content(_ d: UpdateDialog) -> some View {
        switch d {
        case .checking:
            ProgressView().progressViewStyle(.linear).tint(Color.csAccent)
        case .available(_, _, let notes, _, _):
            if let notes {
                VStack(alignment: .leading, spacing: 4) {
                    Text("What's new").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ScrollView {
                        Text(Self.markdown(notes))
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
                Text(detail).font(.caption).foregroundStyle(.tertiary)
            }
        case .installing(_, let step):
            HStack(spacing: 10) {
                ProgressView(value: step.fraction).progressViewStyle(.linear).tint(Color.csAccent)
                Text("\(Int((step.fraction * 100).rounded()))%")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(width: 34, alignment: .trailing)
            }
            .accessibilityElement(children: .combine)
            .accessibilityLabel(step.label)
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
            case .upToDate, .installed:
                Spacer()
                primary("OK") { updates.closeDialog() }
            case .checkFailed:
                Spacer()
                Button("OK") { updates.closeDialog() }.keyboardShortcut(.cancelAction)
                primary("Try Again") { updates.checkNow() }
            case .available(_, _, _, _, let page):
                if let page {
                    Button("Release Notes on GitHub") { NSWorkspace.shared.open(page) }
                        .buttonStyle(.link)
                        .font(.callout)
                }
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

    /// Inline Markdown, keeping the release's line breaks.
    static func markdown(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(s)
    }
}
