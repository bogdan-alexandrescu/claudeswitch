// swift-tools-version:5.9
// The menu-bar app. Builds with the Command Line Tools alone (no Xcode
// project): `swift build -c release`, then ../install-app.sh wraps the binary
// in an .app bundle.
import PackageDescription

let package = Package(
    name: "ClaudeSwitchBar",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "ClaudeSwitchBar", targets: ["ClaudeSwitchBar"]),
    ],
    targets: [
        // Models, decoding, formatting and running the binary: everything
        // testable without a window server.
        .target(name: "ClaudeSwitchCore"),
        .executableTarget(name: "ClaudeSwitchBar", dependencies: ["ClaudeSwitchCore"]),
        .testTarget(
            name: "ClaudeSwitchCoreTests",
            dependencies: ["ClaudeSwitchCore"],
            resources: [.copy("Fixtures")]
        ),
    ]
)
