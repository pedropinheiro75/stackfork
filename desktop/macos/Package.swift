// swift-tools-version: 6.2
import PackageDescription

// AppKit, WebKit, ServiceManagement and Foundation only; no package dependency.
let package = Package(
    name: "Stackfork",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "Stackfork", path: "Sources/Stackfork"),
        .testTarget(name: "StackforkTests", dependencies: ["Stackfork"], path: "Tests/StackforkTests"),
    ]
)
