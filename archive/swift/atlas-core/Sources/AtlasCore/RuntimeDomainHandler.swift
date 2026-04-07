import Foundation
import NIOHTTP1

// MARK: - RuntimeDomainHandler

/// A handler responsible for a specific domain of HTTP routes.
///
/// Each domain handler owns a cohesive set of routes (e.g., conversations,
/// approvals, features). The runtime dispatcher tries each handler in order
/// and returns the first non-nil response.
///
/// A Go runtime replacing this Swift implementation must provide a handler for
/// each domain contract below.
protocol RuntimeDomainHandler: Sendable {
    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse?
}

// MARK: - EncodedResponse

/// A serialized HTTP response ready to be written to a NIO channel.
struct EncodedResponse {
    let status: HTTPResponseStatus
    let payload: Data
    var contentType: String = "application/json; charset=utf-8"
    var redirectLocation: String?
    /// Extra response headers (e.g. `Set-Cookie`). Applied after standard headers.
    var additionalHeaders: [(String, String)] = []
}

// MARK: - RuntimeAPIError

enum RuntimeAPIError: LocalizedError {
    case invalidRequest(String)
    case notFound(String)
    case forbidden(String)
    case unauthorized(String)

    var errorDescription: String? {
        switch self {
        case .invalidRequest(let message),
             .notFound(let message),
             .forbidden(let message),
             .unauthorized(let message):
            return message
        }
    }
}
