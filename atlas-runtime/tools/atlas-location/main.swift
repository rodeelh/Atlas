// atlas-location: CoreLocation helper for Project Atlas.
//
// Requests the device's current location via CoreLocation (uses WiFi
// positioning + GPS hardware when available), prints a single JSON line to
// stdout, then exits.
//
// Output on success:
//   {"latitude":37.33,"longitude":-122.03,"accuracy":15.0,"altitude":50.0,"source":"corelocation"}
//
// Output on error:
//   {"error":"Location access denied — grant permission in System Settings"}
//
// Exit code: 0 on success, 1 on error or timeout.
//
// Build + sign (done by Makefile):
//   swiftc -O -o atlas-location main.swift
//   codesign --force --sign - --entitlements location.entitlements atlas-location

import Foundation
import CoreLocation

final class LocationFetcher: NSObject, CLLocationManagerDelegate {
    let manager = CLLocationManager()
    private var finished = false

    override init() {
        super.init()
        manager.delegate = self
        manager.desiredAccuracy = kCLLocationAccuracyBest
    }

    func start() {
        switch manager.authorizationStatus {
        case .authorizedAlways, .authorizedWhenInUse:
            manager.requestLocation()
        case .notDetermined:
            manager.requestWhenInUseAuthorization()
        case .denied, .restricted:
            fail("Location access denied — grant permission in System Settings → Privacy & Security → Location Services")
        @unknown default:
            manager.requestWhenInUseAuthorization()
        }
    }

    // MARK: - CLLocationManagerDelegate

    func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        switch manager.authorizationStatus {
        case .authorizedAlways, .authorizedWhenInUse:
            manager.requestLocation()
        case .denied, .restricted:
            fail("Location access denied — grant permission in System Settings → Privacy & Security → Location Services")
        default:
            break
        }
    }

    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard let loc = locations.first else { return }
        let result: [String: Any] = [
            "latitude":  loc.coordinate.latitude,
            "longitude": loc.coordinate.longitude,
            "accuracy":  loc.horizontalAccuracy,
            "altitude":  loc.altitude,
            "source":    "corelocation"
        ]
        succeed(result)
    }

    func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
        fail(error.localizedDescription)
    }

    // MARK: - Helpers

    private func succeed(_ result: [String: Any]) {
        guard !finished else { return }
        finished = true
        emit(result)
        exit(0)
    }

    private func fail(_ message: String) {
        guard !finished else { return }
        finished = true
        emit(["error": message])
        exit(1)
    }

    private func emit(_ obj: [String: Any]) {
        if let data = try? JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys]),
           let str = String(data: data, encoding: .utf8) {
            print(str)
        }
    }
}

let fetcher = LocationFetcher()
fetcher.start()

// Run for up to 12 seconds, then time out.
RunLoop.main.run(until: Date(timeIntervalSinceNow: 12))

// Timed out — print error and exit.
if let data = try? JSONSerialization.data(withJSONObject: ["error": "Location request timed out after 12 seconds"]),
   let str = String(data: data, encoding: .utf8) {
    print(str)
}
exit(1)
