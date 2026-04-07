import Foundation

extension AppleScriptTemplates {

    static func getNowPlaying(targetApp: String = "Music") -> String {
        return """
        tell application "\(sanitize(targetApp))"
            if player state is stopped then
                return "Player is stopped. Nothing is currently playing."
            end if
            set t to current track
            set output to "TRACK:" & (name of t) & "\\n"
            set output to output & "ARTIST:" & (artist of t) & "\\n"
            set output to output & "ALBUM:" & (album of t) & "\\n"
            set output to output & "DURATION:" & (duration of t) & "\\n"
            set output to output & "POSITION:" & (player position) & "\\n"
            set output to output & "STATE:" & (player state as text) & "\\n"
            return output
        end tell
        """
    }

    static func searchLibrary(
        query: String,
        maxResults: Int,
        targetApp: String = "Music"
    ) -> String {
        return """
        tell application "\(sanitize(targetApp))"
            set results to search playlist "Library" for "\(sanitize(query))"
            set output to ""
            set resultCount to 0
            repeat with t in results
                if resultCount >= \(maxResults) then exit repeat
                set output to output & "TRACK:" & (name of t) & "\\n"
                set output to output & "ARTIST:" & (artist of t) & "\\n"
                set output to output & "ALBUM:" & (album of t) & "\\n"
                set output to output & "---\\n"
                set resultCount to resultCount + 1
            end repeat
            if output is "" then return "No tracks found matching \\"\(sanitize(query))\\"."
            return output
        end tell
        """
    }

    static func play(targetApp: String = "Music") -> String {
        """
        tell application "\(sanitize(targetApp))"
            play
            return "Playback started."
        end tell
        """
    }

    static func pause(targetApp: String = "Music") -> String {
        """
        tell application "\(sanitize(targetApp))"
            pause
            return "Playback paused."
        end tell
        """
    }

    static func nextTrack(targetApp: String = "Music") -> String {
        """
        tell application "\(sanitize(targetApp))"
            next track
            return "Skipped to next track."
        end tell
        """
    }

    static func previousTrack(targetApp: String = "Music") -> String {
        """
        tell application "\(sanitize(targetApp))"
            previous track
            return "Went back to previous track."
        end tell
        """
    }

    static func setVolume(_ level: Int, targetApp: String = "Music") -> String {
        let clamped = min(max(level, 0), 100)
        return """
        tell application "\(sanitize(targetApp))"
            set sound volume to \(clamped)
            return "Volume set to \(clamped)."
        end tell
        """
    }

    static func searchAndPlay(query: String, targetApp: String = "Music") -> String {
        return """
        tell application "\(sanitize(targetApp))"
            set results to search playlist "Library" for "\(sanitize(query))"
            if results is {} then
                return "No tracks found matching \\"\(sanitize(query))\\". Make sure the song is in your Music library."
            end if
            set t to item 1 of results
            play t
            return "Now playing: " & (name of t) & " by " & (artist of t)
        end tell
        """
    }

    static func setShuffle(_ enabled: Bool, targetApp: String = "Music") -> String {
        let value = enabled ? "true" : "false"
        let label = enabled ? "on" : "off"
        return """
        tell application "\(sanitize(targetApp))"
            set shuffle enabled to \(value)
            return "Shuffle turned \(label)."
        end tell
        """
    }
}
