// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "AgentlySDK",
    platforms: [
        .iOS(.v17),
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AgentlySDK",
            targets: ["AgentlySDK"]
        )
    ],
    targets: [
        .target(
            name: "AgentlySDK",
            resources: [.process("Resources")]
        ),
        .testTarget(
            name: "AgentlySDKTests",
            dependencies: ["AgentlySDK"],
            resources: [.process("Fixtures")]
        )
    ]
)
