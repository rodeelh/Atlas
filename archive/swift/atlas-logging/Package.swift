// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasLoggingPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasShared",
            targets: ["AtlasShared"]
        ),
        .library(
            name: "AtlasLogging",
            targets: ["AtlasLogging"]
        )
    ],
    targets: [
        .target(
            name: "AtlasShared"
        ),
        .target(
            name: "AtlasLogging",
            dependencies: ["AtlasShared"]
        ),
        .testTarget(
            name: "AtlasSharedTests",
            dependencies: ["AtlasShared"]
        )
    ]
)
