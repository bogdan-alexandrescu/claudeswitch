import AppKit
import Carbon.HIToolbox
import ClaudeSwitchCore
import SwiftUI

/// The global hotkey for Switch to best on the followed profile (F6):
/// Carbon's RegisterEventHotKey, which needs no Accessibility permission
/// (an NSEvent global monitor would). Off by default; ⌥⌘S suggested.
@MainActor
final class HotKeyCenter: ObservableObject {
    static let enabledKey = "hotKeyEnabled"
    static let keyKey = "hotKey"

    @Published var enabled: Bool {
        didSet {
            UserDefaults.standard.set(enabled, forKey: Self.enabledKey)
            apply()
        }
    }
    @Published var key: HotKey {
        didSet {
            UserDefaults.standard.set(key.stored, forKey: Self.keyKey)
            apply()
        }
    }
    /// Why the hotkey is not registered, when it should be.
    @Published private(set) var problem: String?
    var action: () -> Void = {}

    private var ref: EventHotKeyRef?
    private var handler: EventHandlerRef?

    init() {
        enabled = UserDefaults.standard.bool(forKey: Self.enabledKey)
        key = UserDefaults.standard.string(forKey: Self.keyKey).flatMap(HotKey.init(stored:)) ?? .suggested
    }

    /// Registers the hotkey as set, or unregisters it.
    func apply() {
        if let r = ref {
            UnregisterEventHotKey(r)
            ref = nil
        }
        problem = nil
        guard enabled, !CommandLine.arguments.contains("--render") else { return }
        guard key.isUsable else {
            problem = "Choose a key with ⌘, ⌥ or ⌃."
            return
        }
        installHandler()
        var r: EventHotKeyRef?
        let id = EventHotKeyID(signature: OSType(0x4353_5357), id: 1) // "CSSW"
        let status = RegisterEventHotKey(key.keyCode, key.carbonModifiers, id, GetApplicationEventTarget(), 0, &r)
        if status == noErr {
            ref = r
        } else {
            problem = "\(key.display) is taken by another app or by macOS. Choose another."
        }
    }

    /// Unregisters while a new key is being recorded, so pressing the
    /// current one records it rather than firing it.
    func suspend() {
        if let r = ref {
            UnregisterEventHotKey(r)
            ref = nil
        }
    }

    private func installHandler() {
        guard handler == nil else { return }
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
            Task { @MainActor in AppHub.hotKey.action() }
            return noErr
        }, 1, &spec, nil, &handler)
    }
}

/// Settings row: the hotkey switch and a recorder for the key.
struct HotKeyRow: View {
    @ObservedObject var hotKey: HotKeyCenter
    @State private var recording = false
    @State private var monitor: Any?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent {
                HStack(spacing: 8) {
                    Button(recording ? "Press keys…" : hotKey.key.display) { recording ? stop() : record() }
                        .font(.body.monospaced())
                        .help("Click, then press the keys: ⌘, ⌥ or ⌃ and a key. Escape cancels.")
                        .accessibilityLabel(recording ? "Recording a hotkey" : "Hotkey \(hotKey.key.display)")
                    Toggle("", isOn: $hotKey.enabled).labelsHidden().toggleStyle(.switch)
                        .accessibilityLabel("Switch to best hotkey")
                }
            } label: {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Switch to best hotkey")
                    Text("Anywhere on your Mac, switches the profile the menu bar follows to its best account.")
                        .font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if let p = hotKey.problem {
                Label(p, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
            }
        }
        .onDisappear { stop() }
    }

    func record() {
        recording = true
        hotKey.suspend()
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { e in
            if e.keyCode == UInt16(kVK_Escape) {
                stop()
                return nil
            }
            var mods: HotKeyModifiers = []
            if e.modifierFlags.contains(.command) { mods.insert(.command) }
            if e.modifierFlags.contains(.option) { mods.insert(.option) }
            if e.modifierFlags.contains(.control) { mods.insert(.control) }
            if e.modifierFlags.contains(.shift) { mods.insert(.shift) }
            let k = HotKey(keyCode: UInt32(e.keyCode), modifiers: mods)
            guard k.isUsable else { return nil }
            hotKey.key = k
            stop()
            return nil
        }
    }

    func stop() {
        if let m = monitor { NSEvent.removeMonitor(m) }
        monitor = nil
        if recording {
            recording = false
            hotKey.apply()
        }
    }
}
