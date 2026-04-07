import Foundation
import Darwin

public struct WebDomainPolicy: Sendable {
    private let blockedHostnames: Set<String>
    private let blockedSuffixes: [String]
    private let maxResolvedAddresses: Int

    public init(
        blockedHostnames: Set<String> = [
            "localhost",
            "localhost.localdomain"
        ],
        blockedSuffixes: [String] = [
            ".local",
            ".internal",
            ".home.arpa"
        ],
        maxResolvedAddresses: Int = 8
    ) {
        self.blockedHostnames = Set(blockedHostnames.map { $0.lowercased() })
        self.blockedSuffixes = blockedSuffixes.map { $0.lowercased() }
        self.maxResolvedAddresses = max(1, maxResolvedAddresses)
    }

    public func normalizeQuery(_ query: String) throws -> String {
        let normalized = query
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)

        guard normalized.count >= 2 else {
            throw WebResearchError.invalidQuery
        }

        return normalized
    }

    public func normalizeAllowedDomains(_ allowedDomains: [String]?) -> [String]? {
        guard let allowedDomains, !allowedDomains.isEmpty else {
            return nil
        }

        let values = Set(
            allowedDomains.compactMap { rawValue -> String? in
                normalizeDomain(rawValue)
            }
        )

        return values.isEmpty ? nil : values.sorted()
    }

    public func validateURLString(
        _ rawValue: String,
        allowedDomains: [String]? = nil
    ) throws -> URL {
        let trimmed = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), url.host != nil else {
            throw WebResearchError.invalidURL(rawValue)
        }

        return try validateURL(url, allowedDomains: allowedDomains)
    }

    @discardableResult
    public func validateURL(
        _ url: URL,
        allowedDomains: [String]? = nil,
        resolveDNS: Bool = true
    ) throws -> URL {
        guard let scheme = url.scheme?.lowercased() else {
            throw WebResearchError.invalidURL(url.absoluteString)
        }

        guard scheme == "http" || scheme == "https" else {
            throw WebResearchError.unsupportedScheme(scheme)
        }

        guard let host = url.host?.lowercased(), !host.isEmpty else {
            throw WebResearchError.invalidURL(url.absoluteString)
        }

        try validateHost(host, allowedDomains: allowedDomains, resolveDNS: resolveDNS)
        return url
    }

    public func validateHost(
        _ host: String,
        allowedDomains: [String]? = nil,
        resolveDNS: Bool = true
    ) throws {
        let normalizedHost = host.lowercased()

        if blockedHostnames.contains(normalizedHost) || blockedSuffixes.contains(where: normalizedHost.hasSuffix) {
            throw WebResearchError.blockedHost(normalizedHost)
        }

        if let ipv4 = parseIPv4(normalizedHost) {
            guard isBlockedIPv4(ipv4) == false else {
                throw WebResearchError.blockedIPAddress(normalizedHost)
            }
        } else if let ipv6 = parseIPv6(normalizedHost) {
            guard isBlockedIPv6(ipv6) == false else {
                throw WebResearchError.blockedIPAddress(normalizedHost)
            }
        }

        if let allowedDomains = normalizeAllowedDomains(allowedDomains),
           allowedDomains.contains(where: { domainMatches(normalizedHost, allowedDomain: $0) }) == false {
            throw WebResearchError.disallowedDomain(normalizedHost)
        }

        if resolveDNS {
            for address in resolvedAddresses(for: normalizedHost) {
                switch address {
                case .ipv4(let bytes) where isBlockedIPv4(bytes):
                    throw WebResearchError.blockedIPAddress(normalizedHost)
                case .ipv6(let bytes) where isBlockedIPv6(bytes):
                    throw WebResearchError.blockedIPAddress(normalizedHost)
                default:
                    continue
                }
            }
        }
    }

    public func domainMatches(_ host: String, allowedDomain: String) -> Bool {
        host == allowedDomain || host.hasSuffix(".\(allowedDomain)")
    }

    private func normalizeDomain(_ rawValue: String) -> String? {
        let trimmed = rawValue
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased()

        guard !trimmed.isEmpty else {
            return nil
        }

        if let url = URL(string: trimmed), let host = url.host?.lowercased() {
            return host
        }

        return trimmed
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .isEmpty ? nil : trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "."))
    }

    private func parseIPv4(_ host: String) -> [UInt8]? {
        let components = host.split(separator: ".")
        guard components.count == 4 else {
            return nil
        }

        let octets = components.compactMap { UInt8($0) }
        return octets.count == 4 ? octets : nil
    }

    private func parseIPv6(_ host: String) -> [UInt8]? {
        var storage = in6_addr()
        return host.withCString { value in
            inet_pton(AF_INET6, value, &storage) == 1 ? withUnsafeBytes(of: storage) { Array($0) } : nil
        }
    }

    private func isBlockedIPv4(_ octets: [UInt8]) -> Bool {
        guard octets.count == 4 else { return true }

        let first = octets[0]
        let second = octets[1]

        return first == 0 ||
            first == 10 ||
            first == 127 ||
            (first == 100 && (64...127).contains(Int(second))) ||
            (first == 169 && second == 254) ||
            (first == 172 && (16...31).contains(Int(second))) ||
            (first == 192 && second == 168) ||
            (first == 198 && (18...19).contains(Int(second)))
    }

    private func isBlockedIPv6(_ octets: [UInt8]) -> Bool {
        guard octets.count == 16 else { return true }

        if octets.allSatisfy({ $0 == 0 }) {
            return true
        }

        if octets.dropLast().allSatisfy({ $0 == 0 }) && octets.last == 1 {
            return true
        }

        if octets[0] == 0xfe && (octets[1] & 0xc0) == 0x80 {
            return true
        }

        if (octets[0] & 0xfe) == 0xfc {
            return true
        }

        let v4MappedPrefix = Array(repeating: UInt8(0), count: 10) + [0xff, 0xff]
        if Array(octets.prefix(12)) == v4MappedPrefix {
            return isBlockedIPv4(Array(octets.suffix(4)))
        }

        return false
    }

    private enum ResolvedAddress {
        case ipv4([UInt8])
        case ipv6([UInt8])
    }

    private func resolvedAddresses(for host: String) -> [ResolvedAddress] {
        var hints = addrinfo(
            ai_flags: AI_ADDRCONFIG,
            ai_family: AF_UNSPEC,
            ai_socktype: SOCK_STREAM,
            ai_protocol: IPPROTO_TCP,
            ai_addrlen: 0,
            ai_canonname: nil,
            ai_addr: nil,
            ai_next: nil
        )

        var resultPointer: UnsafeMutablePointer<addrinfo>?
        let status = getaddrinfo(host, nil, &hints, &resultPointer)
        guard status == 0, let resultPointer else {
            return []
        }

        defer { freeaddrinfo(resultPointer) }

        var currentPointer: UnsafeMutablePointer<addrinfo>? = resultPointer
        var addresses: [ResolvedAddress] = []

        while let current = currentPointer, addresses.count < maxResolvedAddresses {
            let info = current.pointee

            if info.ai_family == AF_INET,
               let rawAddress = info.ai_addr {
                let address = rawAddress.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { pointer in
                    withUnsafeBytes(of: pointer.pointee.sin_addr) { Array($0) }
                }
                addresses.append(.ipv4(address))
            } else if info.ai_family == AF_INET6,
                      let rawAddress = info.ai_addr {
                let address = rawAddress.withMemoryRebound(to: sockaddr_in6.self, capacity: 1) { pointer in
                    withUnsafeBytes(of: pointer.pointee.sin6_addr) { Array($0) }
                }
                addresses.append(.ipv6(address))
            }

            currentPointer = info.ai_next
        }

        return addresses
    }
}
