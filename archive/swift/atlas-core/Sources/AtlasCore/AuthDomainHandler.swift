import Foundation
import NIOHTTP1
import AtlasShared

// MARK: - AuthDomainHandler

/// Handles auth bootstrap, web static serving, and CORS preflight.
///
/// Routes owned:
///   OPTIONS  *                    — CORS preflight
///   GET      /                    — redirect → /web
///   GET      /web                 — web UI static
///   GET      /web/**              — web UI static assets
///   GET      /auth/token          — issue launch token (native app)
///   GET      /auth/bootstrap      — exchange token → session cookie → /web
///   GET      /auth/ping           — diagnostic HTML ping
///   GET      /auth/remote-gate    — remote login page
///   GET      /auth/remote         — API key auth → session cookie → /web
///   GET      /auth/remote-status  — LAN access info (authenticated)
///   GET      /auth/remote-key     — remote access token (native app / authenticated)
///   DELETE   /auth/remote-sessions — revoke all remote sessions
struct AuthDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        // CORS preflight — CORS headers are appended by the call site
        if method == .OPTIONS {
            return EncodedResponse(status: .ok, payload: Data(), contentType: "text/plain")
        }

        // Root redirect → /web
        if method == .GET, path == "/" {
            return EncodedResponse(status: .found, payload: Data(), contentType: "text/plain", redirectLocation: "/web")
        }

        // Web static serving
        if method == .GET, path == "/web" || path.hasPrefix("/web/") {
            return routeWebStatic(path: path)
        }

        // Auth routes
        guard path.hasPrefix("/auth/") else { return nil }

        switch (method, path) {
        case (.GET, "/auth/token"):
            let token = await runtime.issueWebLaunchToken()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(["token": token]))

        case (.GET, "/auth/bootstrap"):
            guard let token = queryItems["token"] else {
                throw RuntimeAPIError.invalidRequest("Missing 'token' query parameter.")
            }
            let (_, cookieHeader) = try await runtime.bootstrapWebSession(token: token)
            var response = EncodedResponse(
                status: .found,
                payload: Data(),
                contentType: "text/plain",
                redirectLocation: "/web"
            )
            response.additionalHeaders = [("Set-Cookie", cookieHeader)]
            return response

        case (.GET, "/auth/ping"):
            let html = "<html><body style='font-family:monospace;padding:32px'><h2>✓ Atlas is reachable</h2><p>Server: \(Bundle.main.bundleIdentifier ?? "AtlasRuntimeService")</p><p>Time: \(Date())</p><script>document.write('<p>JS works ✓</p><p>Origin: '+location.origin+'</p><p>Host: '+location.host+'</p>')</script></body></html>"
            return EncodedResponse(status: .ok, payload: Data(html.utf8), contentType: "text/html")

        case (.GET, "/auth/remote-gate"):
            return EncodedResponse(status: .ok, payload: Data(Self.remoteGateHTML().utf8), contentType: "text/html")

        case (.GET, "/auth/remote"):
            guard let key = queryItems["key"], !key.isEmpty else {
                throw RuntimeAPIError.invalidRequest("Missing 'key' query parameter.")
            }
            guard let cookieHeader = await runtime.authenticateRemoteAPIKey(key) else {
                throw RuntimeAPIError.unauthorized("Invalid API key or remote access is disabled.")
            }
            var response = EncodedResponse(
                status: .found,
                payload: Data(),
                contentType: "text/plain",
                redirectLocation: "/web"
            )
            response.additionalHeaders = [("Set-Cookie", cookieHeader)]
            return response

        // Remote access status — requires auth when called from a browser.
        // NOTE: NOT covered by the /auth/* blanket exemption; carries sensitive data.
        case (.GET, "/auth/remote-status"):
            if let origin = headers["Origin"].first {
                let sessionID = WebAuthService.sessionID(fromCookieHeader: headers["Cookie"].first)
                guard await runtime.validateWebSession(id: sessionID) else {
                    throw RuntimeAPIError.unauthorized("Authentication required.")
                }
                if !isLocalhostOrigin(origin) {
                    let lanEnabled = await runtime.remoteAccessEnabled()
                    guard lanEnabled else {
                        throw RuntimeAPIError.unauthorized("LAN access is disabled.")
                    }
                }
            }
            let statusInfo = await runtime.remoteAccessStatus()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(statusInfo))

        // Remote access token — NOT covered by /auth/* blanket exemption.
        case (.GET, "/auth/remote-key"):
            if headers["Origin"].first != nil {
                let sessionID = WebAuthService.sessionID(fromCookieHeader: headers["Cookie"].first)
                guard await runtime.validateWebSession(id: sessionID) else {
                    throw RuntimeAPIError.unauthorized("Authentication required.")
                }
            }
            let key = await runtime.remoteAccessKey()
            struct KeyPayload: Encodable { let key: String }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(KeyPayload(key: key)))

        case (.DELETE, "/auth/remote-sessions"):
            await runtime.revokeAndRotateRemoteAccess()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(["revoked": true]))

        default:
            return nil
        }
    }

    // MARK: - Web Static

    private func routeWebStatic(path: String) -> EncodedResponse {
        let subPath: String
        if path == "/web" || path == "/web/" {
            subPath = "/index.html"
        } else {
            subPath = String(path.dropFirst("/web".count))
        }

        guard let (mimeType, data) = WebStaticHandler.response(for: subPath) else {
            return EncodedResponse(status: .serviceUnavailable, payload: Data("Web UI not bundled.".utf8), contentType: "text/plain")
        }

        return EncodedResponse(status: .ok, payload: data, contentType: mimeType)
    }

    // MARK: - Remote gate HTML

    private static func remoteGateHTML() -> String {
        """
        <!DOCTYPE html>
        <html lang="en">
        <head>
          <meta charset="UTF-8">
          <meta name="viewport" content="width=device-width, initial-scale=1">
          <title>Atlas — Connect</title>
          <style>
            *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
            body {
              font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
              background: #f5f5f7; color: #1d1d1f;
              min-height: 100svh; display: flex; align-items: center; justify-content: center;
            }
            .card {
              background: #fff; border-radius: 18px;
              padding: 40px 36px; width: 100%; max-width: 360px;
              box-shadow: 0 4px 24px rgba(0,0,0,0.08);
            }
            .logo { font-size: 28px; font-weight: 700; letter-spacing: -0.5px; margin-bottom: 6px; }
            .sub  { font-size: 14px; color: #6e6e73; margin-bottom: 28px; }
            label { display: block; font-size: 13px; font-weight: 500; margin-bottom: 6px; }
            input {
              width: 100%; padding: 10px 13px; border-radius: 10px;
              border: 1px solid #d2d2d7; font-size: 15px; font-family: ui-monospace, monospace;
              outline: none; transition: border-color 0.15s;
            }
            input:focus { border-color: #0071e3; }
            button {
              margin-top: 16px; width: 100%; padding: 11px;
              background: #0071e3; color: #fff; border: none; border-radius: 10px;
              font-size: 15px; font-weight: 600; cursor: pointer; transition: background 0.15s;
            }
            button:hover { background: #0077ed; }
            .hint { margin-top: 16px; font-size: 12px; color: #6e6e73; text-align: center; }
          </style>
        </head>
        <body>
          <div class="card">
            <div class="logo">Atlas</div>
            <div class="sub">Enter your access token to connect</div>
            <form id="f" onsubmit="connect(event)">
              <label for="k">Access token</label>
              <input id="k" type="password" placeholder="Paste your token here" autocomplete="off" autofocus>
              <button type="submit">Connect</button>
            </form>
            <p class="hint">Find your access token in the Atlas app under Settings → Network.</p>
          </div>
          <script>
            if (location.protocol === 'https:' && location.hostname !== 'localhost' && location.hostname !== '127.0.0.1') {
              location.replace('http://' + location.host + location.pathname + location.search + location.hash);
            }

            function connect(e) {
              e.preventDefault();
              const key = document.getElementById('k').value.trim();
              if (key) window.location.href = 'http://' + location.host + '/auth/remote?key=' + encodeURIComponent(key);
            }
          </script>
        </body>
        </html>
        """
    }

    // MARK: - Helpers

    private func isLocalhostOrigin(_ origin: String) -> Bool {
        origin.hasPrefix("http://localhost") || origin.hasPrefix("http://127.0.0.1")
    }
}
