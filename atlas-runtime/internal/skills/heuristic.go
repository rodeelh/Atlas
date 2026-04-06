package skills

// Micro-heuristic tool selection
//
// Scores user messages against capability groups using three tiers of evidence:
//
//   Tier 1 — phrases  (+3 pts): multi-word expressions; substring match.
//                                Most specific signal — high confidence.
//   Tier 2 — pairs    (+2 pts): (verb, object) token pairs; both must appear as
//                                whole words anywhere in the message.
//                                Captures intent: "check"+"weather" → weather group.
//   Tier 3 — words    (+1 pt):  single tokens; matched at word boundaries via
//                                tokenizer. Negation-aware.
//
// Each tier contributes at most its point value per group (first match in the
// tier fires and that tier is done). Group activation is controlled by per-group
// thresholds defined in groupThresholds in registry.go.

import (
	"regexp"
	"strings"
)

// wordRe extracts lowercase alphanumeric tokens from a string.
var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// tokenize returns the ordered slice of lowercase word tokens in s.
func tokenize(s string) []string {
	return wordRe.FindAllString(strings.ToLower(s), -1)
}

// negationMarkers are tokens that negate the following 1–2 words.
var negationMarkers = map[string]bool{
	"not": true, "no": true, "dont": true, "without": true,
	"never": true, "cant": true, "wont": true, "shouldnt": true,
	"stop": true, "avoid": true, "skip": true, "ignore": true,
}

// negatedSet returns the set of tokens that appear within 2 positions after a
// negation marker. These are excluded from word-level and pair-level matching.
func negatedSet(tokens []string) map[string]bool {
	neg := make(map[string]bool)
	for i, t := range tokens {
		if negationMarkers[t] {
			for j := i + 1; j < i+3 && j < len(tokens); j++ {
				neg[tokens[j]] = true
			}
		}
	}
	return neg
}

// groupSignals describes how to detect intent for one capability group.
type groupSignals struct {
	phrases []string    // multi-word; substring match (+3 pts)
	words   []string    // single whole-word tokens; negation-aware (+1 pt)
	pairs   [][2]string // (tokenA, tokenB); both whole-word, non-negated (+2 pts)
}

// intentSignals maps each scored group to its detection signals.
// "core" is always-on and has no entry here.
var intentSignals = map[string]groupSignals{

	// ── meta (atlas runtime status) ──────────────────────────────────────────
	"meta": {
		phrases: []string{
			"atlas status", "are you running", "what version", "runtime info",
			"atlas version", "how are you doing", "system status",
		},
		words: []string{
			"atlas", "runtime", "version",
		},
		pairs: [][2]string{
			{"atlas", "status"}, {"atlas", "version"}, {"are", "running"},
			{"runtime", "info"}, {"system", "status"},
		},
	},

	// ── weather ──────────────────────────────────────────────────────────────
	"weather": {
		phrases: []string{
			"what's the weather", "how's the weather", "weather forecast",
			"weather today", "weather tomorrow", "what's it like outside",
			"will it rain", "is it going to snow", "chance of rain",
			"current weather", "weather this week",
		},
		words: []string{
			"weather", "forecast", "temperature", "rain", "snow",
			"wind", "sunny", "cloudy", "humidity", "storm",
			"hail", "drizzle", "overcast", "celsius", "fahrenheit",
		},
		pairs: [][2]string{
			{"check", "weather"}, {"get", "weather"}, {"what", "weather"},
			{"how", "weather"}, {"get", "forecast"}, {"check", "forecast"},
			{"will", "rain"}, {"going", "rain"}, {"going", "snow"},
			{"chance", "rain"}, {"weather", "week"},
		},
	},

	// ── web ──────────────────────────────────────────────────────────────────
	"web": {
		phrases: []string{
			"search for", "search online", "search the web", "look up",
			"find information", "latest news", "read this article",
			"fetch this url", "what does this page say", "check this link",
			"summarize this article", "what does this website say",
			"find me an article", "search google",
			"what's happening", "what is happening", "whats happening",
			"what's going on", "whats going on", "what is going on",
			"news about", "news in", "events in", "things to do in",
			"what to do in", "happening in", "going on in",
			"tell me about", "what do you know about",
		},
		words: []string{
			"search", "news", "article", "wikipedia", "google",
			"research", "find",
		},
		pairs: [][2]string{
			{"search", "for"}, {"look", "up"}, {"find", "online"},
			{"get", "news"}, {"latest", "news"}, {"read", "article"},
			{"check", "link"}, {"fetch", "url"}, {"search", "web"},
			{"find", "article"}, {"search", "news"}, {"find", "information"},
			{"what", "happening"}, {"what", "going"}, {"news", "about"},
			{"happening", "today"}, {"going", "today"}, {"events", "today"},
			{"tell", "about"}, {"know", "about"},
		},
	},

	// ── finance ──────────────────────────────────────────────────────────────
	"finance": {
		phrases: []string{
			"stock price", "share price", "market cap", "exchange rate",
			"crypto price", "stock market", "how much is", "trading at",
			"convert currency", "what currency", "currency in",
		},
		words: []string{
			"stock", "crypto", "bitcoin", "ethereum", "nasdaq",
			"portfolio", "dividend", "forex", "shares", "etf",
		},
		pairs: [][2]string{
			{"stock", "price"}, {"check", "stock"}, {"crypto", "price"},
			{"bitcoin", "price"}, {"exchange", "rate"}, {"market", "cap"},
			{"get", "quote"}, {"stock", "market"}, {"convert", "currency"},
			{"currency", "rate"}, {"market", "price"},
		},
	},

	// ── office ───────────────────────────────────────────────────────────────
	"office": {
		phrases: []string{
			"send an email", "send a message to", "schedule a meeting",
			"add to calendar", "add a calendar event", "create a reminder",
			"check my email", "check my calendar", "what's on my calendar",
			"add a note", "find contact", "look up contact",
			"new calendar event", "set a reminder", "my inbox",
			"upcoming meetings", "write an email",
		},
		words: []string{
			"email", "calendar", "reminder", "meeting", "appointment",
			"inbox", "contact", "notes",
		},
		pairs: [][2]string{
			{"send", "email"}, {"check", "email"}, {"read", "email"},
			{"write", "email"}, {"schedule", "meeting"}, {"add", "calendar"},
			{"create", "reminder"}, {"set", "reminder"}, {"check", "calendar"},
			{"find", "contact"}, {"add", "note"}, {"write", "note"},
			{"check", "reminders"}, {"new", "event"}, {"add", "reminder"},
			{"my", "calendar"}, {"my", "email"}, {"my", "inbox"},
		},
	},

	// ── media ────────────────────────────────────────────────────────────────
	"media": {
		phrases: []string{
			"play music", "what's playing", "open safari",
			"macos version", "system info", "what version is",
			"pause the music", "skip this song",
		},
		words: []string{
			"music", "safari", "itunes", "playlist", "song", "track", "album",
		},
		pairs: [][2]string{
			{"play", "music"}, {"pause", "music"}, {"stop", "music"},
			{"open", "safari"}, {"macos", "version"}, {"system", "info"},
			{"what", "playing"}, {"skip", "song"}, {"next", "song"},
			{"current", "song"}, {"music", "playing"},
		},
	},

	// ── mac ──────────────────────────────────────────────────────────────────
	"mac": {
		phrases: []string{
			"open an app", "send a notification", "reveal in finder",
			"copy to clipboard", "open in finder", "open the app",
			"what apps are running", "bring to front",
		},
		words: []string{
			"notification", "clipboard", "finder", "application",
		},
		pairs: [][2]string{
			{"open", "app"}, {"open", "application"}, {"launch", "app"},
			{"send", "notification"}, {"copy", "clipboard"}, {"read", "clipboard"},
			{"reveal", "finder"}, {"quit", "app"}, {"running", "apps"},
			{"activate", "app"}, {"open", "finder"}, {"show", "finder"},
		},
	},

	// ── shell ────────────────────────────────────────────────────────────────
	"shell": {
		phrases: []string{
			"run a command", "run this command", "run the command",
			"run a script", "run this script", "execute a command",
			"shell command", "run in terminal", "kill this process",
			"kill the process", "what processes are running",
			"run applescript", "execute applescript",
		},
		words: []string{
			"terminal", "bash", "zsh", "applescript",
		},
		pairs: [][2]string{
			{"run", "command"}, {"execute", "command"}, {"run", "script"},
			{"kill", "process"}, {"list", "processes"}, {"check", "processes"},
			{"run", "applescript"}, {"execute", "script"}, {"shell", "script"},
			{"run", "terminal"}, {"terminal", "command"},
		},
	},

	// ── files ────────────────────────────────────────────────────────────────
	"files": {
		phrases: []string{
			"read this file", "read the file", "write to file",
			"create a file", "list files", "find files", "search in files",
			"save to disk", "create a folder", "read the contents of",
			"what's in this file", "write to disk",
		},
		words: []string{
			"file", "folder", "directory", "disk", "csv", "pdf", "config", "log",
		},
		pairs: [][2]string{
			{"read", "file"}, {"write", "file"}, {"create", "file"},
			{"delete", "file"}, {"list", "files"}, {"find", "file"},
			{"save", "file"}, {"create", "folder"}, {"search", "files"},
			{"read", "folder"}, {"list", "directory"}, {"write", "disk"},
			{"read", "csv"}, {"parse", "csv"}, {"read", "pdf"},
		},
	},

	// ── vault ────────────────────────────────────────────────────────────────
	"vault": {
		phrases: []string{
			"store my password", "save my password", "store api key",
			"save api key", "my credentials", "login credentials",
			"stored credentials", "generate 2fa code", "generate totp",
			"store this secret", "save this secret",
		},
		words: []string{
			"password", "credential", "secret", "totp", "2fa", "vault",
		},
		pairs: [][2]string{
			{"store", "password"}, {"save", "password"}, {"store", "credential"},
			{"lookup", "credential"}, {"my", "credentials"}, {"store", "key"},
			{"api", "key"}, {"generate", "totp"}, {"my", "password"},
			{"login", "credential"}, {"store", "secret"}, {"save", "credential"},
		},
	},

	// ── browser ──────────────────────────────────────────────────────────────
	"browser": {
		phrases: []string{
			"navigate to", "open in browser", "go to the website",
			"login to", "log in to", "fill out the form", "fill in the form",
			"take a screenshot", "browse to", "click on the button",
			"interact with", "web automation", "automate the browser",
		},
		words: []string{
			"website", "browser", "navigate", "screenshot", "captcha",
			"webpage", "browse",
		},
		pairs: [][2]string{
			{"open", "website"}, {"go", "website"}, {"navigate", "page"},
			{"click", "button"}, {"fill", "form"}, {"take", "screenshot"},
			{"login", "site"}, {"browse", "website"}, {"open", "url"},
			{"visit", "site"}, {"open", "tab"}, {"new", "tab"},
			{"submit", "form"}, {"navigate", "url"},
		},
	},

	// ── creative ─────────────────────────────────────────────────────────────
	"creative": {
		phrases: []string{
			"generate an image", "create an image", "make a picture",
			"generate a picture", "draw a", "design a", "generate art",
			"create artwork", "edit this image", "dalle",
		},
		words: []string{
			"illustration", "artwork", "dalle",
		},
		pairs: [][2]string{
			{"generate", "image"}, {"create", "image"}, {"make", "image"},
			{"generate", "picture"}, {"create", "picture"}, {"edit", "image"},
			{"design", "image"}, {"draw", "image"}, {"make", "art"},
		},
	},

	// ── automation ───────────────────────────────────────────────────────────
	"automation": {
		phrases: []string{
			"create automation", "new automation", "list automations",
			"run automation", "schedule automation", "my automations",
			"set up automation", "enable automation", "disable automation",
		},
		words: []string{
			"automation", "gremlin", "recurring", "trigger",
		},
		pairs: [][2]string{
			{"create", "automation"}, {"new", "automation"}, {"list", "automations"},
			{"run", "automation"}, {"schedule", "automation"}, {"set", "automation"},
			{"enable", "automation"}, {"disable", "automation"}, {"delete", "automation"},
			{"my", "automations"},
		},
	},

	// ── forge ────────────────────────────────────────────────────────────────
	"forge": {
		phrases: []string{
			"build a skill", "create a skill", "make a skill",
			"add a skill", "new skill", "forge a skill", "install a skill",
			"build me a skill", "create a new skill",
		},
		words: []string{
			"forge",
		},
		pairs: [][2]string{
			{"build", "skill"}, {"create", "skill"}, {"make", "skill"},
			{"add", "skill"}, {"new", "skill"}, {"forge", "skill"},
			{"install", "skill"},
		},
	},
}

// scoreGroups scores a user message against all keyword-triggered groups.
// Returns a map of group → cumulative score. Callers compare against groupThresholds.
//
// Each tier contributes at most its point value per group — the first match
// in a tier fires and that tier is done.
func scoreGroups(message string) map[string]int {
	lower := strings.ToLower(message)
	tokens := tokenize(lower)

	tokenSet := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = true
	}
	negated := negatedSet(tokens)

	scores := make(map[string]int, len(intentSignals))

	for group, sig := range intentSignals {
		score := 0

		// Tier 1 — phrase matching (+3 pts; first match wins)
		for _, phrase := range sig.phrases {
			if strings.Contains(lower, phrase) {
				score += 3
				break
			}
		}

		// Tier 2 — pair matching (+2 pts; first match wins)
		for _, pair := range sig.pairs {
			if tokenSet[pair[0]] && tokenSet[pair[1]] &&
				!negated[pair[0]] && !negated[pair[1]] {
				score += 2
				break
			}
		}

		// Tier 3 — word matching (+1 pt; first match wins)
		for _, word := range sig.words {
			if tokenSet[word] && !negated[word] {
				score += 1
				break
			}
		}

		scores[group] = score
	}

	return scores
}
