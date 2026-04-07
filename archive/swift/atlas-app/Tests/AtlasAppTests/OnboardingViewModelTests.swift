import XCTest
import AtlasShared
@testable import AtlasApp

@MainActor
final class OnboardingViewModelTests: XCTestCase {
    func testDefaultAssistantNameFallsBackToAtlas() {
        let viewModel = OnboardingViewModel(
            locale: Locale(identifier: "en_US"),
            timeZone: TimeZone(identifier: "America/New_York") ?? .autoupdatingCurrent,
            userName: "Rami",
            assistantName: "   "
        )

        XCTAssertEqual(viewModel.assistantName, "Atlas")
    }

    func testUSLocationInfersFahrenheitAndTwelveHourTime() {
        let derived = OnboardingViewModel.derivePreferences(
            from: "Orlando, United States",
            locale: Locale(identifier: "en_GB")
        )

        XCTAssertEqual(derived.temperatureUnit, "Fahrenheit")
        XCTAssertEqual(derived.timeFormat, "12-hour time")
        XCTAssertEqual(derived.dateFormat, "Month/Day/Year")
    }

    func testUnitedKingdomLocationInfersCelsiusAndTwentyFourHourTime() {
        let derived = OnboardingViewModel.derivePreferences(
            from: "London, United Kingdom",
            locale: Locale(identifier: "en_US")
        )

        XCTAssertEqual(derived.temperatureUnit, "Celsius")
        XCTAssertEqual(derived.timeFormat, "24-hour time")
        XCTAssertEqual(derived.dateFormat, "Day/Month/Year")
    }

    func testIdentityStepBlocksEmptyAssistantName() {
        let viewModel = OnboardingViewModel(
            locale: Locale(identifier: "en_US"),
            timeZone: TimeZone(identifier: "America/New_York") ?? .autoupdatingCurrent
        )
        viewModel.currentStep = .identity
        viewModel.assistantName = " "

        viewModel.goForward()

        XCTAssertEqual(viewModel.currentStep, .identity)
        XCTAssertEqual(viewModel.errorMessage, "Choose what you'd like Atlas to be called.")
    }

    func testOnboardingStepSetMatchesNineStepWizard() {
        XCTAssertEqual(
            OnboardingViewModel.Step.allCases,
            [.welcome, .identity, .aiProvider, .channels, .skillKeys, .permissions, .network, .installService, .ready]
        )
    }
}
