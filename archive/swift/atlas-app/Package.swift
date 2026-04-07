// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasAppPackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .executable(
            name: "AtlasApp",
            targets: ["AtlasApp"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-core"),
        .package(path: "../atlas-logging"),
        .package(path: "../atlas-memory"),
        .package(path: "../atlas-network"),
        .package(path: "../atlas-skills")
    ],
    targets: [
        .executableTarget(
            name: "AtlasApp",
            dependencies: [
                .product(name: "AtlasCore", package: "atlas-core"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging"),
                .product(name: "AtlasNetwork", package: "atlas-network"),
                .product(name: "AtlasSkills", package: "atlas-skills")
            ]
        ),
        .testTarget(
            name: "AtlasAppTests",
            dependencies: [
                "AtlasApp",
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasMemory", package: "atlas-memory")
            ]
        )
    ]
)
