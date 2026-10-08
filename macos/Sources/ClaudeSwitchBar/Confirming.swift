import ClaudeSwitchCore
import SwiftUI

/// An action waiting for its confirmation.
struct PendingAction: Identifiable {
    let id = UUID()
    let confirmation: Confirmation
    let run: () -> Void
}

extension View {
    /// Asks before running `pending`, with its title, message and button.
    func confirming(_ pending: Binding<PendingAction?>) -> some View {
        confirmationDialog(pending.wrappedValue?.confirmation.title ?? "",
                           isPresented: Binding(get: { pending.wrappedValue != nil },
                                                set: { if !$0 { pending.wrappedValue = nil } }),
                           titleVisibility: .visible,
                           presenting: pending.wrappedValue) { p in
            Button(p.confirmation.action, role: p.confirmation.destructive ? .destructive : nil) { p.run() }
            Button("Cancel", role: .cancel) {}
        } message: { p in
            Text(p.confirmation.message)
        }
    }
}
