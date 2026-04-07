import Foundation

extension AppleScriptTemplates {

    static func listRecentMessages(
        mailbox: String?,
        count: Int
    ) -> String {
        let mailboxBlock: String
        if let mb = mailbox {
            mailboxBlock = """
            set targetMailbox to missing value
            repeat with acct in accounts
                try
                    set targetMailbox to mailbox "\(sanitize(mb))" of acct
                    exit repeat
                end try
            end repeat
            if targetMailbox is missing value then
                return "Mailbox \\"\(sanitize(mb))\\" not found."
            end if
            set theMessages to (messages of targetMailbox)
            """
        } else {
            mailboxBlock = """
            set theMessages to {}
            repeat with acct in accounts
                try
                    set inboxMessages to messages of inbox of acct
                    repeat with m in inboxMessages
                        set end of theMessages to m
                    end repeat
                end try
            end repeat
            """
        }

        return """
        tell application "Mail"
            \(mailboxBlock)
            set output to ""
            set resultCount to 0
            repeat with m in theMessages
                if resultCount >= \(count) then exit repeat
                set output to output & "FROM:" & (sender of m) & "\\n"
                set output to output & "SUBJECT:" & (subject of m) & "\\n"
                set output to output & "DATE:" & ((date received of m) as text) & "\\n"
                set output to output & "READ:" & ((read status of m) as text) & "\\n"
                set output to output & "---\\n"
                set resultCount to resultCount + 1
            end repeat
            if output is "" then return "No messages found."
            return output
        end tell
        """
    }

    static func searchMessages(
        query: String,
        mailbox: String?,
        maxResults: Int
    ) -> String {
        let mailboxBlock: String
        if let mb = mailbox {
            mailboxBlock = """
            set theMessages to {}
            repeat with acct in accounts
                try
                    set mbMessages to messages of mailbox "\(sanitize(mb))" of acct
                    repeat with m in mbMessages
                        set end of theMessages to m
                    end repeat
                end try
            end repeat
            """
        } else {
            mailboxBlock = """
            set theMessages to {}
            repeat with acct in accounts
                try
                    repeat with mb in mailboxes of acct
                        repeat with m in (messages of mb)
                            set end of theMessages to m
                        end repeat
                    end repeat
                end try
            end repeat
            """
        }

        return """
        tell application "Mail"
            \(mailboxBlock)
            set searchQuery to "\(sanitize(query))"
            set output to ""
            set resultCount to 0
            repeat with m in theMessages
                if resultCount >= \(maxResults) then exit repeat
                set subj to subject of m
                set sndr to sender of m
                if (subj contains searchQuery) or (sndr contains searchQuery) then
                    set output to output & "FROM:" & sndr & "\\n"
                    set output to output & "SUBJECT:" & subj & "\\n"
                    set output to output & "DATE:" & ((date received of m) as text) & "\\n"
                    set output to output & "READ:" & ((read status of m) as text) & "\\n"
                    set output to output & "---\\n"
                    set resultCount to resultCount + 1
                end if
            end repeat
            if output is "" then return "No messages found matching \\"\(sanitize(query))\\"."
            return output
        end tell
        """
    }

    /// Compose a mail message. When autoSend is false (default) the message is saved
    /// silently to Drafts. When true the message is sent immediately.
    static func composeMail(
        to recipient: String,
        subject: String,
        body: String,
        autoSend: Bool
    ) -> String {
        let dispatchLine = autoSend
            ? "send newMessage\n    return \"Sent message \\\"\\(sanitize(subject))\\\" to \\(sanitize(recipient)).\""
            : "return \"Draft created: \\\"\\(sanitize(subject))\\\" addressed to \\(sanitize(recipient)).\""

        return """
        tell application "Mail"
            set newMessage to make new outgoing message with properties ¬
                {subject:"\(sanitize(subject))", content:"\(sanitize(body))", visible:false}
            tell newMessage
                make new to recipient with properties {address:"\(sanitize(recipient))"}
            end tell
            \(dispatchLine)
        end tell
        """
    }

    /// Read the full body of the first message whose subject contains the given string.
    static func readMessageBody(
        subject: String,
        mailbox: String?,
        maxBodyChars: Int
    ) -> String {
        let gatherBlock: String
        if let mb = mailbox {
            gatherBlock = """
            set theMessages to {}
            repeat with acct in accounts
                try
                    set mbMessages to messages of mailbox "\(sanitize(mb))" of acct
                    repeat with m in mbMessages
                        set end of theMessages to m
                    end repeat
                end try
            end repeat
            """
        } else {
            gatherBlock = """
            set theMessages to {}
            repeat with acct in accounts
                try
                    set inboxMsgs to messages of inbox of acct
                    repeat with m in inboxMsgs
                        set end of theMessages to m
                    end repeat
                end try
            end repeat
            """
        }

        return """
        tell application "Mail"
            \(gatherBlock)
            set searchSubject to "\(sanitize(subject))"
            repeat with m in theMessages
                if (subject of m) contains searchSubject then
                    set msgBody to content of m
                    try
                        set msgBody to text 1 thru \(maxBodyChars) of msgBody
                        set msgBody to msgBody & "...[truncated]"
                    end try
                    set output to "FROM:" & (sender of m) & "\\n"
                    set output to output & "SUBJECT:" & (subject of m) & "\\n"
                    set output to output & "DATE:" & ((date received of m) as text) & "\\n"
                    set output to output & "BODY:" & msgBody
                    return output
                end if
            end repeat
            return "No message found with subject containing \\"\(sanitize(subject))\\"."
        end tell
        """
    }
}
