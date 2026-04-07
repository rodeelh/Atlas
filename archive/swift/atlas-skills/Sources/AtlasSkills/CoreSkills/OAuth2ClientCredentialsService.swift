import Foundation
import AtlasLogging

// MARK: - OAuth2Error

public enum OAuth2Error: LocalizedError, Sendable {
    case missingTokenURL
    case missingClientID
    case missingClientSecret
    case tokenEndpointFailed(Int, String?)
    case missingAccessToken

    public var errorDescription: String? {
        switch self {
        case .missingTokenURL:
            return "oauth2ClientCredentials plan is missing oauth2TokenURL."
        case .missingClientID:
            return "oauth2ClientCredentials plan is missing oauth2ClientIDKey."
        case .missingClientSecret:
            return "oauth2ClientCredentials plan is missing oauth2ClientSecretKey."
        case .tokenEndpointFailed(let status, let body):
            return "Token endpoint returned HTTP \(status)\(body.map { ": \($0)" } ?? "")."
        case .missingAccessToken:
            return "Token endpoint response did not contain access_token."
        }
    }
}

// MARK: - OAuth2ClientCredentialsService

/// Fetches and caches OAuth 2.0 Client Credentials tokens for use in Forge skills.
///
/// Stateless — all state lives in `OAuth2TokenCache.shared`.
/// Injected with `CoreHTTPService` and `CoreSecretsService` so secrets never leave Keychain.
///
/// Security rules enforced here:
/// - `clientID`, `clientSecret`, and `accessToken` are NEVER logged at any level.
/// - Only structural facts are logged: token endpoint host, HTTP status, cache hit/miss.
public struct OAuth2ClientCredentialsService: Sendable {
    private let httpService: CoreHTTPService
    private let secretsService: CoreSecretsService
    private let logger: AtlasLogger

    public init(
        httpService: CoreHTTPService,
        secretsService: CoreSecretsService,
        logger: AtlasLogger = AtlasLogger(category: "forge.oauth2")
    ) {
        self.httpService = httpService
        self.secretsService = secretsService
        self.logger = logger
    }

    /// Returns a valid access token for the given plan, using the cache when possible.
    ///
    /// Cache key is "\(oauth2TokenURL)|\(oauth2ClientIDKey)" — unique per API + credential pair.
    public func fetchToken(plan: HTTPRequestPlan) async throws -> String {
        guard let tokenURL = plan.oauth2TokenURL, !tokenURL.isEmpty else {
            throw OAuth2Error.missingTokenURL
        }
        guard let clientIDKey = plan.oauth2ClientIDKey, !clientIDKey.isEmpty else {
            throw OAuth2Error.missingClientID
        }
        guard let clientSecretKey = plan.oauth2ClientSecretKey, !clientSecretKey.isEmpty else {
            throw OAuth2Error.missingClientSecret
        }

        let cacheKey = "\(tokenURL)|\(clientIDKey)"

        if let cached = await OAuth2TokenCache.shared.token(for: cacheKey) {
            logger.info("OAuth2 token cache hit", metadata: [
                "token_host": URL(string: tokenURL)?.host ?? "unknown"
            ])
            return cached
        }

        logger.info("OAuth2 token cache miss — exchanging client credentials", metadata: [
            "token_host": URL(string: tokenURL)?.host ?? "unknown"
        ])

        guard let clientID = try await secretsService.get(service: clientIDKey) else {
            throw OAuth2Error.missingClientID
        }
        guard let clientSecret = try await secretsService.get(service: clientSecretKey) else {
            throw OAuth2Error.missingClientSecret
        }

        return try await exchangeClientCredentials(
            tokenURL: tokenURL,
            clientID: clientID,
            clientSecret: clientSecret,
            cacheKey: cacheKey,
            scope: plan.oauth2Scope
        )
    }

    // MARK: - Private

    private func exchangeClientCredentials(
        tokenURL: String,
        clientID: String,
        clientSecret: String,
        cacheKey: String,
        scope: String?
    ) async throws -> String {
        guard let url = URL(string: tokenURL) else {
            throw OAuth2Error.missingTokenURL
        }

        var formFields = "grant_type=client_credentials"
            + "&client_id=\(urlEncode(clientID))"
            + "&client_secret=\(urlEncode(clientSecret))"
        if let scope, !scope.isEmpty {
            formFields += "&scope=\(urlEncode(scope))"
        }

        let request = CoreHTTPRequest(
            url: url,
            method: .post,
            headers: ["Content-Type": "application/x-www-form-urlencoded"],
            body: formFields.data(using: .utf8)
        )

        let response = try await httpService.execute(request)

        logger.info("OAuth2 token endpoint responded", metadata: [
            "status": "\(response.statusCode)",
            "host": url.host ?? "unknown"
        ])

        guard response.isSuccess else {
            throw OAuth2Error.tokenEndpointFailed(
                response.statusCode,
                response.bodyString.map { String($0.prefix(200)) }
            )
        }

        guard
            let data = response.bodyString?.data(using: .utf8),
            let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let accessToken = json["access_token"] as? String, !accessToken.isEmpty
        else {
            throw OAuth2Error.missingAccessToken
        }

        let expiresIn = (json["expires_in"] as? Int) ?? 3600
        await OAuth2TokenCache.shared.store(token: accessToken, expiresIn: expiresIn, for: cacheKey)

        logger.info("OAuth2 token exchange succeeded", metadata: [
            "expires_in": "\(expiresIn)",
            "host": url.host ?? "unknown"
        ])

        return accessToken
    }

    private func urlEncode(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? s
    }
}
