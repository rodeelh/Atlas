// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasSkillsPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasSkills",
            targets: ["AtlasSkills"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-guard"),
        .package(path: "../atlas-logging"),
        .package(path: "../atlas-tools")
    ],
    targets: [
        .target(
            name: "AtlasSkills",
            dependencies: [
                .product(name: "AtlasGuard", package: "atlas-guard"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging"),
                .product(name: "AtlasTools", package: "atlas-tools")
            ],
            path: "Sources/AtlasSkills"
        ),
        .testTarget(
            name: "AtlasSkillsTests",
            dependencies: [
                "AtlasSkills",
                .product(name: "AtlasGuard", package: "atlas-guard"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasTools", package: "atlas-tools")
            ],
            path: "Tests/AtlasSkillsTests"
        )
    ]
)
