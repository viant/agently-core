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
            name: "AgentlySDK"
        ),
        .testTarget(
            name: "AgentlySDKTests",
            dependencies: ["AgentlySDK"]
        )
    ]
)
