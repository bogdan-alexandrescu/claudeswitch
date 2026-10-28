import Foundation

/// New pool as it runs (NewPoolPlan): `pool create`, then a `pool move` for
/// each account already in a pool. Once the pool exists, trying again makes
/// only the moves left: `pool create` would refuse the name now.
public struct NewPoolRun: Equatable {
    public enum Step: Equatable {
        /// `pool create <name> <accounts>`.
        case create(String, accounts: [String])
        /// `pool move <account> --to <pool>`.
        case move(String, to: String)

        public func run(_ cli: CLI) -> Result<PoolList, CallError> {
            switch self {
            case .create(let n, let a): return cli.poolCreate(n, accounts: a)
            case .move(let a, let to): return cli.poolMove(a, to: to)
            }
        }
    }

    public let plan: NewPoolPlan
    /// `pool create` succeeded: the pool exists.
    public private(set) var created = false
    /// The accounts moved in so far.
    public private(set) var moved: [String] = []

    public init(_ plan: NewPoolPlan) { self.plan = plan }

    /// The steps still to take, in order.
    public var steps: [Step] {
        let moves = plan.moves.compactMap { d -> Step? in
            guard case .move(let a, _, let to) = d, !moved.contains(a) else { return nil }
            return .move(a, to: to)
        }
        return (created ? [] : [.create(plan.name, accounts: plan.create)]) + moves
    }

    /// A step succeeded.
    public mutating func done(_ s: Step) {
        switch s {
        case .create: created = true
        case .move(let a, _): moved.append(a)
        }
    }

    /// The sheet's button: Create ("Create…" when accounts leave their
    /// pools, since it asks first), then, once the pool exists, the moves
    /// left.
    public var action: String {
        guard created else { return plan.confirmation == nil ? "Create" : "Create…" }
        let left = steps
        if left.count == 1, case .move(let a, _) = left[0] { return "Move \(inQuotes(a)) In" }
        return "Move the Rest In"
    }

    /// Once the pool exists there is nothing to cancel: it stays.
    public var cancel: String { created ? "Close" : "Cancel" }

    /// What a refused step says; a move's, that the pool was created.
    public func failure(_ s: Step, _ message: String) -> String {
        switch s {
        case .create: return message
        case .move(let a, _):
            return "The pool \(inQuotes(plan.name)) was created, but \(inQuotes(a)) could not be moved in: " + message
        }
    }
}
