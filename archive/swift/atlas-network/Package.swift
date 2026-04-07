// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasNetworkPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasNetwork",
            targets: ["AtlasNetwork"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-logging")
    ],
    targets: [
        .target(
            name: "AtlasNetwork",
            dependencies: [
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging")
            ]
        )
    ]
)
