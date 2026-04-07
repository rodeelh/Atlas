import Foundation

/// In-session cache for fetched web resources. Lives inside `WebResearchOrchestrator`
/// and deduplicates redundant fetches within a single research call or across multiple
/// calls during one agent turn. TTL-based eviction, max 20 entries.
actor WebFetchCache {
    private struct Entry {
        let resource: WebFetchedResource
        let storedAt: Date
    }

    private var entries: [URL: Entry] = [:]
    private let ttlSeconds: TimeInterval
    private let maxEntries: Int

    init(ttlSeconds: TimeInterval = 300, maxEntries: Int = 20) {
        self.ttlSeconds = ttlSeconds
        self.maxEntries = maxEntries
    }

    func get(_ url: URL) -> WebFetchedResource? {
        guard let entry = entries[url] else { return nil }
        if Date.now.timeIntervalSince(entry.storedAt) > ttlSeconds {
            entries.removeValue(forKey: url)
            return nil
        }
        return entry.resource
    }

    func set(_ url: URL, resource: WebFetchedResource) {
        if entries.count >= maxEntries {
            if let oldestKey = entries.min(by: { $0.value.storedAt < $1.value.storedAt })?.key {
                entries.removeValue(forKey: oldestKey)
            }
        }
        entries[url] = Entry(resource: resource, storedAt: .now)
    }
}
