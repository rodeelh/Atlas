// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasGuardPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasGuard",
            targets: ["AtlasGuard"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-logging")
    ],
    targets: [
        .target(
            name: "AtlasGuard",
            dependencies: [
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging")
            ]
        )
    ]
)
