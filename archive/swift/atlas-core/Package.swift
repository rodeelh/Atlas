// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AtlasCorePackage",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .library(
            name: "AtlasCore",
            targets: ["AtlasCore"]
        ),
        .executable(
            name: "AtlasRuntimeService",
            targets: ["AtlasRuntimeService"]
        )
    ],
    dependencies: [
        .package(path: "../atlas-bridges"),
        .package(path: "../atlas-guard"),
        .package(path: "../atlas-logging"),
        .package(path: "../atlas-memory"),
        .package(path: "../atlas-network"),
        .package(path: "../atlas-skills"),
        .package(path: "../atlas-tools"),
        .package(url: "https://github.com/apple/swift-nio.git", from: "2.73.0")
    ],
    targets: [
        .target(
            name: "AtlasCore",
            dependencies: [
                .product(name: "AtlasBridges", package: "atlas-bridges"),
                .product(name: "AtlasGuard", package: "atlas-guard"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasLogging", package: "atlas-logging"),
                .product(name: "AtlasMemory", package: "atlas-memory"),
                .product(name: "AtlasNetwork", package: "atlas-network"),
                .product(name: "AtlasSkills", package: "atlas-skills"),
                .product(name: "AtlasTools", package: "atlas-tools"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOHTTP1", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio")
            ]
        ),
        .executableTarget(
            name: "AtlasRuntimeService",
            dependencies: ["AtlasCore"],
            // Info.plist and .entitlements are embedded/applied at Xcode build-phase
            // time (via -Xlinker -sectcreate and codesign). SPM does not process them.
            exclude: ["Info.plist", "AtlasRuntimeService.entitlements"],
            resources: [
                .copy("com.projectatlas.runtime.plist"),
                .copy("web")
            ]
        ),
        .testTarget(
            name: "AtlasCoreTests",
            dependencies: [
                "AtlasCore",
                .product(name: "AtlasSkills", package: "atlas-skills"),
                .product(name: "AtlasShared", package: "atlas-logging"),
                .product(name: "AtlasMemory", package: "atlas-memory"),
                .product(name: "AtlasNetwork", package: "atlas-network")
            ]
        )
    ]
)
