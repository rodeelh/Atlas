import Foundation
import AtlasLogging

// MARK: - Request / Response

/// A structured HTTP request for internal CoreSkills use.
/// Not part of the user-facing skill system — internal infrastructure only.
public struct CoreHTTPRequest: Sendable {
    public enum Method: String, Sendable {
        case get    = "GET"
        case post   = "POST"
        case put    = "PUT"
        case delete = "DELETE"
    }

    public let url: URL
    public let method: Method
    public let headers: [String: String]
    public let body: Data?
    public let timeoutSeconds: Double

    public init(
        url: URL,
        method: Method = .get,
        headers: [String: String] = [:],
        body: Data? = nil,
        timeoutSeconds: Double = 30
    ) {
        self.url = url
        self.method = method
        self.headers = headers
        self.body = body
        self.timeoutSeconds = timeoutSeconds
    }
}

public struct CoreHTTPResponse: Sendable {
    public let statusCode: Int
    public let headers: [String: String]
    public let body: Data
    public let url: URL

    public init(statusCode: Int, headers: [String: String], body: Data, url: URL) {
        self.statusCode = statusCode
        self.headers = headers
        self.body = body
        self.url = url
    }

    public var isSuccess: Bool { (200..<300).contains(statusCode) }
    public var bodyString: String? { String(data: body, encoding: .utf8) }
}

public enum CoreHTTPError: LocalizedError, Sendable {
    case invalidURL(String)
    case networkError(String)
    case httpError(Int, String?)
    case timeout
    case responseError(String)

    public var errorDescription: String? {
        switch self {
        case .invalidURL(let u):       return "Invalid URL: \(u)"
        case .networkError(let msg):   return "Network error: \(msg)"
        case .httpError(let c, let b): return "HTTP \(c)\(b.map { ": \($0)" } ?? "")"
        case .timeout:                 return "Request timed out"
        case .responseError(let msg):  return "Response error: \(msg)"
        }
    }
}

// MARK: - CoreHTTPService

/// Internal HTTP execution primitive for CoreSkills, built-in skills, and Forge.
/// Wraps URLSession with structured request/response types, consistent logging,
/// and normalized errors. Not exposed in the user-facing skill registry.
public struct CoreHTTPService: Sendable {
    private let logger: AtlasLogger

    public init(logger: AtlasLogger = AtlasLogger(category: "core.http")) {
        self.logger = logger
    }

    /// Execute a structured HTTP request and return the raw response.
    public func execute(_ request: CoreHTTPRequest) async throws -> CoreHTTPResponse {
        var urlRequest = URLRequest(url: request.url, timeoutInterval: request.timeoutSeconds)
        urlRequest.httpMethod = request.method.rawValue
        urlRequest.httpBody = request.body
        for (key, value) in request.headers {
            urlRequest.setValue(value, forHTTPHeaderField: key)
        }

        logger.info("CoreHTTP executing request", metadata: [
            "method": request.method.rawValue,
            "host": request.url.host ?? "unknown",
            "path": request.url.path
        ])

        do {
            let (data, response) = try await URLSession.shared.data(for: urlRequest)
            guard let http = response as? HTTPURLResponse else {
                throw CoreHTTPError.responseError("Non-HTTP response received")
            }

            let headers: [String: String] = http.allHeaderFields.reduce(into: [:]) { acc, pair in
                if let k = pair.key as? String, let v = pair.value as? String {
                    acc[k] = v
                }
            }

            logger.info("CoreHTTP request completed", metadata: [
                "status": "\(http.statusCode)",
                "bytes": "\(data.count)"
            ])

            return CoreHTTPResponse(
                statusCode: http.statusCode,
                headers: headers,
                body: data,
                url: http.url ?? request.url
            )
        } catch let error as CoreHTTPError {
            throw error
        } catch let urlError as URLError where urlError.code == .timedOut {
            throw CoreHTTPError.timeout
        } catch {
            throw CoreHTTPError.networkError(error.localizedDescription)
        }
    }

    /// Fetch the text content of a URL for research or extraction use.
    /// Returns the raw body string. Throws if the response is not 2xx or not UTF-8.
    public func fetchPage(url: URL, timeoutSeconds: Double = 30) async throws -> String {
        let request = CoreHTTPRequest(url: url, timeoutSeconds: timeoutSeconds)
        let response = try await execute(request)
        guard response.isSuccess else {
            throw CoreHTTPError.httpError(response.statusCode, nil)
        }
        guard let text = response.bodyString else {
            throw CoreHTTPError.responseError("Response body is not valid UTF-8 text")
        }
        return text
    }
}
