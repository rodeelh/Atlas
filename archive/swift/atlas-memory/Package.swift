// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasMemoryPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasMemory",
            targets: ["AtlasMemory"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-logging"),
        .package(url: "https://github.com/stephencelis/SQLite.swift.git", from: "0.15.0")
    ],
    targets: [
        .target(
            name: "AtlasMemory",
            dependencies: [
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging"),
                .product(name: "SQLite", package: "SQLite.swift")
            ]
        ),
        .testTarget(
            name: "AtlasMemoryTests",
            dependencies: [
                "AtlasMemory",
                .product(name: "AtlasShared", package: "atlas-logging")
            ]
        )
    ]
)
