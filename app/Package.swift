// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "KubitApp",
    platforms: [.macOS("27.0")],
    products: [
        .executable(name: "KubitApp", targets: ["KubitApp"])
    ],
    targets: [
        .executableTarget(name: "KubitApp", path: "Sources/KubitApp"),
        .testTarget(name: "KubitAppTests", dependencies: ["KubitApp"], path: "Tests/KubitAppTests", exclude: ["Fixtures"])
    ]
)
