import Foundation
import AtlasTools

// MARK: - Protocol

protocol AppleScriptExecuting: Sendable {
    func run(_ script: String, timeout: TimeInterval) async throws -> String
}

// MARK: - Executor

struct AppleScriptExecutor: AppleScriptExecuting {

    func run(_ script: String, timeout: TimeInterval) async throws -> String {
        let clampedTimeout = min(max(timeout, 1), 60)

        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
            process.arguments = ["-e", script]

            let stdoutPipe = Pipe()
            let stderrPipe = Pipe()
            process.standardOutput = stdoutPipe
            process.standardError = stderrPipe

            // Single-resume guard — prevents both the timer and terminationHandler
            // from racing to resume the continuation.
            let guard_ = ResumeOnce()

            // Timeout via DispatchSource so we can cancel it from the termination handler.
            let timer = DispatchSource.makeTimerSource(queue: .global())
            timer.schedule(deadline: .now() + clampedTimeout)
            timer.setEventHandler {
                timer.cancel()
                if process.isRunning { process.terminate() }
                guard_.resume {
                    continuation.resume(throwing: AtlasToolError.executionFailed(
                        "Script timed out after \(Int(clampedTimeout))s. The target application may be unresponsive."
                    ))
                }
            }

            process.terminationHandler = { proc in
                timer.cancel()
                let stdout = (try? stdoutPipe.fileHandleForReading.readToEnd())
                    .flatMap { String(data: $0, encoding: .utf8) }?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                let stderr = (try? stderrPipe.fileHandleForReading.readToEnd())
                    .flatMap { String(data: $0, encoding: .utf8) }?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

                guard_.resume {
                    if proc.terminationStatus != 0 {
                        continuation.resume(throwing: Self.mapError(stderr: stderr, stdout: stdout))
                    } else {
                        let output = stdout.count > 4096
                            ? String(stdout.prefix(4096)) + "\n[output truncated]"
                            : stdout
                        continuation.resume(returning: output)
                    }
                }
            }

            do {
                timer.resume()
                try process.run()
            } catch {
                timer.cancel()
                guard_.resume {
                    continuation.resume(throwing: AtlasToolError.executionFailed(
                        "Failed to launch osascript: \(error.localizedDescription)"
                    ))
                }
            }
        }
    }

    // MARK: - Error mapping

    private static func mapError(stderr: String, stdout: String) -> Error {
        let message = stderr.isEmpty ? stdout : stderr

        // TCC / Automation permission error codes
        let tccCodes = ["-1743", "-1719", "-10004"]
        for code in tccCodes where message.contains(code) {
            let appName = extractAppName(from: message) ?? "the requested application"
            let daemonName = ProcessInfo.processInfo.processName  // e.g. "AtlasRuntimeService"
            return AtlasToolError.executionFailed(
                "Automation access denied for \(appName). In System Settings > Privacy & Security > Automation, find \"\(daemonName)\" and enable \(appName). If \"\(daemonName)\" is not listed, run: tccutil reset AppleEvents — then retry so macOS can prompt for permission."
            )
        }

        return AtlasToolError.executionFailed(
            message.isEmpty ? "osascript exited with a non-zero status." : message
        )
    }

    private static func extractAppName(from message: String) -> String? {
        guard let range = message.range(of: "application \"") else { return nil }
        let after = message[range.upperBound...]
        guard let end = after.firstIndex(of: "\"") else { return nil }
        return String(after[after.startIndex..<end])
    }
}

// MARK: - Helpers

/// Thread-safe single-fire gate. Prevents double-resume of a continuation when
/// both a timeout DispatchSource and a Process.terminationHandler race.
private final class ResumeOnce: @unchecked Sendable {
    private let lock = NSLock()
    private var fired = false

    func resume(_ action: () -> Void) {
        lock.lock()
        defer { lock.unlock() }
        guard !fired else { return }
        fired = true
        action()
    }
}
