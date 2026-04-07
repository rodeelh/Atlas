// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasBridgesPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasBridges",
            targets: ["AtlasBridges"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-network"),
        .package(path: "../atlas-memory"),
        .package(path: "../atlas-logging")
    ],
    targets: [
        .target(
            name: "AtlasBridges",
            dependencies: [
                .product(name: "AtlasNetwork", package: "atlas-network"),
                .product(name: "AtlasMemory", package: "atlas-memory"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging")
            ]
        ),
        .testTarget(
            name: "AtlasBridgesTests",
            dependencies: [
                "AtlasBridges",
                .product(name: "AtlasMemory", package: "atlas-memory"),
                .product(name: "AtlasNetwork", package: "atlas-network"),
                .product(name: "AtlasShared", package: "atlas-logging")
            ]
        )
    ]
)
