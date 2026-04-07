// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasToolsPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasTools",
            targets: ["AtlasTools"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-guard"),
        .package(path: "../atlas-logging")
    ],
    targets: [
        .target(
            name: "AtlasTools",
            dependencies: [
                .product(name: "AtlasGuard", package: "atlas-guard"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging")
            ]
        ),
        .testTarget(
            name: "AtlasToolsTests",
            dependencies: [
                "AtlasTools",
                .product(name: "AtlasGuard", package: "atlas-guard"),
                .product(name: "AtlasShared", package: "atlas-logging")
            ]
        )
    ]
)
