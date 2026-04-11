package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/location"
)

const (
	nominatimBase   = "https://nominatim.openstreetmap.org"
	googleMapsBase  = "https://maps.googleapis.com/maps/api"
	mapsHTTPTimeout = 10 * time.Second
	mapsUserAgent   = "ProjectAtlas/1.0"
)

func mapsHTTPGet(ctx context.Context, rawURL, userAgent string) ([]byte, error) {
	client := &http.Client{Timeout: mapsHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (r *Registry) registerMaps() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.geocode",
			Description: "Convert an address or place name to geographic coordinates (latitude/longitude).",
			Properties: map[string]ToolParam{
				"address": {Description: "Address or place name to geocode (e.g. '1600 Pennsylvania Ave, Washington DC')", Type: "string"},
			},
			Required: []string{"address"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsGeocodeResult,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.reverse_geocode",
			Description: "Convert geographic coordinates (latitude/longitude) to a human-readable address.",
			Properties: map[string]ToolParam{
				"latitude":  {Description: "Latitude coordinate", Type: "number"},
				"longitude": {Description: "Longitude coordinate", Type: "number"},
			},
			Required: []string{"latitude", "longitude"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsReverseGeocodeResult,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.search",
			Description: "Search for places, businesses, or points of interest by name or category. Returns names, addresses, ratings, and open status. Uses Google Places when a key is configured, otherwise OpenStreetMap.",
			Properties: map[string]ToolParam{
				"query": {Description: "Search query (e.g. 'coffee shops near downtown Seattle', 'Italian restaurants in Riyadh', 'hospitals near me')", Type: "string"},
				"max":   {Description: "Maximum number of results to return (default 5, max 10)", Type: "integer"},
			},
			Required: []string{"query"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsSearchResult,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.directions",
			Description: "Get turn-by-turn directions between two locations including distance, duration, and step-by-step instructions. Requires Google Maps API key.",
			Properties: map[string]ToolParam{
				"origin":      {Description: "Starting address or place name", Type: "string"},
				"destination": {Description: "Destination address or place name", Type: "string"},
				"mode": {
					Description: "Travel mode (default: driving)",
					Type:        "string",
					Enum:        []string{"driving", "walking", "bicycling", "transit"},
				},
			},
			Required: []string{"origin", "destination"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsDirectionsResult,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.distance",
			Description: "Get the distance and estimated travel time between two locations. Requires Google Maps API key.",
			Properties: map[string]ToolParam{
				"origin":      {Description: "Starting address or place name", Type: "string"},
				"destination": {Description: "Destination address or place name", Type: "string"},
				"mode": {
					Description: "Travel mode (default: driving)",
					Type:        "string",
					Enum:        []string{"driving", "walking", "bicycling", "transit"},
				},
			},
			Required: []string{"origin", "destination"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsDistanceResult,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "maps.my_location",
			Description: "Get Atlas's current known location (city, country, coordinates, timezone). Uses IP-based geolocation — refreshes if the cached location is stale.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		FnResult:    mapsMyLocationResult,
	})
}

// ── maps.geocode ──────────────────────────────────────────────────────────────

func mapsGeocode(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Address == "" {
		return "", fmt.Errorf("address is required")
	}

	u := nominatimBase + "/search?q=" + url.QueryEscape(p.Address) + "&format=json&limit=3&addressdetails=1"
	data, err := mapsHTTPGet(ctx, u, mapsUserAgent)
	if err != nil {
		return "", fmt.Errorf("geocoding failed: %w", err)
	}

	var results []struct {
		DisplayName string `json:"display_name"`
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return "", fmt.Errorf("geocoding parse failed: %w", err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", p.Address), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Geocoding results for \"%s\":\n\n", p.Address))
	for i, r := range results {
		lat, _ := strconv.ParseFloat(r.Lat, 64)
		lon, _ := strconv.ParseFloat(r.Lon, 64)
		sb.WriteString(fmt.Sprintf("%d. %s\n   Coordinates: %.6f, %.6f\n\n", i+1, r.DisplayName, lat, lon))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── maps.reverse_geocode ──────────────────────────────────────────────────────

func mapsReverseGeocode(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("latitude and longitude are required")
	}

	u := fmt.Sprintf("%s/reverse?lat=%.6f&lon=%.6f&format=json", nominatimBase, p.Latitude, p.Longitude)
	data, err := mapsHTTPGet(ctx, u, mapsUserAgent)
	if err != nil {
		return "", fmt.Errorf("reverse geocoding failed: %w", err)
	}

	var result struct {
		DisplayName string `json:"display_name"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("reverse geocoding parse failed: %w", err)
	}
	if result.Error != "" {
		return fmt.Sprintf("Location not found: %s", result.Error), nil
	}
	return fmt.Sprintf("Address for (%.6f, %.6f):\n%s", p.Latitude, p.Longitude, result.DisplayName), nil
}

// ── maps.search ───────────────────────────────────────────────────────────────

func mapsSearch(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
		Max   int    `json:"max"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if p.Max <= 0 {
		p.Max = 5
	}
	if p.Max > 10 {
		p.Max = 10
	}

	bundle, _ := creds.Read()
	if bundle.GoogleMapsAPIKey != "" {
		return mapsSearchGoogle(ctx, p.Query, p.Max, bundle.GoogleMapsAPIKey)
	}
	return mapsSearchNominatim(ctx, p.Query, p.Max)
}

func mapsSearchGoogle(ctx context.Context, query string, max int, apiKey string) (string, error) {
	u := googleMapsBase + "/place/textsearch/json?query=" + url.QueryEscape(query) + "&key=" + url.QueryEscape(apiKey)
	data, err := mapsHTTPGet(ctx, u, "")
	if err != nil {
		return "", fmt.Errorf("places search failed: %w", err)
	}

	var result struct {
		Status  string `json:"status"`
		Results []struct {
			Name             string  `json:"name"`
			FormattedAddress string  `json:"formatted_address"`
			Rating           float64 `json:"rating"`
			UserRatingsTotal int     `json:"user_ratings_total"`
			OpeningHours     *struct {
				OpenNow bool `json:"open_now"`
			} `json:"opening_hours"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("places search parse failed: %w", err)
	}
	if result.Status == "REQUEST_DENIED" || result.Status == "INVALID_REQUEST" {
		return "", fmt.Errorf("places search error: %s — check Google Maps API key in Settings → Credentials", result.Status)
	}
	if result.Status != "OK" && result.Status != "ZERO_RESULTS" {
		return "", fmt.Errorf("places search error: %s", result.Status)
	}
	if len(result.Results) == 0 {
		return fmt.Sprintf("No places found for: %s", query), nil
	}

	count := len(result.Results)
	if count > max {
		count = max
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Places matching \"%s\":\n\n", query))
	for i := 0; i < count; i++ {
		r := result.Results[i]
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Name))
		sb.WriteString(fmt.Sprintf("   Address: %s\n", r.FormattedAddress))
		if r.Rating > 0 {
			sb.WriteString(fmt.Sprintf("   Rating: %.1f/5.0 (%d reviews)\n", r.Rating, r.UserRatingsTotal))
		}
		if r.OpeningHours != nil {
			if r.OpeningHours.OpenNow {
				sb.WriteString("   Status: Open now\n")
			} else {
				sb.WriteString("   Status: Closed\n")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func mapsSearchNominatim(ctx context.Context, query string, max int) (string, error) {
	u := nominatimBase + "/search?q=" + url.QueryEscape(query) +
		"&format=json&limit=" + strconv.Itoa(max) + "&addressdetails=1&extratags=1"
	data, err := mapsHTTPGet(ctx, u, mapsUserAgent)
	if err != nil {
		return "", fmt.Errorf("place search failed: %w", err)
	}

	var results []struct {
		DisplayName string            `json:"display_name"`
		Lat         string            `json:"lat"`
		Lon         string            `json:"lon"`
		Type        string            `json:"type"`
		Extratags   map[string]string `json:"extratags"`
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return "", fmt.Errorf("place search parse failed: %w", err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No places found for: %s", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Places matching \"%s\" (via OpenStreetMap):\n\n", query))
	for i, r := range results {
		lat, _ := strconv.ParseFloat(r.Lat, 64)
		lon, _ := strconv.ParseFloat(r.Lon, 64)
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.DisplayName))
		sb.WriteString(fmt.Sprintf("   Coordinates: %.6f, %.6f | Type: %s\n", lat, lon, r.Type))
		if website, ok := r.Extratags["website"]; ok && website != "" {
			sb.WriteString(fmt.Sprintf("   Website: %s\n", website))
		}
		if phone, ok := r.Extratags["phone"]; ok && phone != "" {
			sb.WriteString(fmt.Sprintf("   Phone: %s\n", phone))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── maps.directions ───────────────────────────────────────────────────────────

func mapsDirections(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Origin      string `json:"origin"`
		Destination string `json:"destination"`
		Mode        string `json:"mode"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Origin == "" || p.Destination == "" {
		return "", fmt.Errorf("origin and destination are required")
	}
	if p.Mode == "" {
		p.Mode = "driving"
	}

	bundle, _ := creds.Read()
	if bundle.GoogleMapsAPIKey == "" {
		return "", fmt.Errorf("Google Maps API key not configured — add it in Settings → Credentials")
	}

	u := googleMapsBase + "/directions/json?" +
		"origin=" + url.QueryEscape(p.Origin) +
		"&destination=" + url.QueryEscape(p.Destination) +
		"&mode=" + url.QueryEscape(p.Mode) +
		"&key=" + url.QueryEscape(bundle.GoogleMapsAPIKey)

	data, err := mapsHTTPGet(ctx, u, "")
	if err != nil {
		return "", fmt.Errorf("directions request failed: %w", err)
	}

	var result struct {
		Status string `json:"status"`
		Routes []struct {
			Summary string `json:"summary"`
			Legs    []struct {
				StartAddress string `json:"start_address"`
				EndAddress   string `json:"end_address"`
				Distance     struct{ Text string `json:"text"` } `json:"distance"`
				Duration     struct{ Text string `json:"text"` } `json:"duration"`
				Steps        []struct {
					HTMLInstructions string                       `json:"html_instructions"`
					Distance         struct{ Text string `json:"text"` } `json:"distance"`
					Duration         struct{ Text string `json:"text"` } `json:"duration"`
				} `json:"steps"`
			} `json:"legs"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("directions parse failed: %w", err)
	}
	if result.Status == "NOT_FOUND" || result.Status == "ZERO_RESULTS" {
		return fmt.Sprintf("No route found from %s to %s.", p.Origin, p.Destination), nil
	}
	if result.Status != "OK" {
		return "", fmt.Errorf("directions error: %s", result.Status)
	}
	if len(result.Routes) == 0 || len(result.Routes[0].Legs) == 0 {
		return fmt.Sprintf("No directions found from %s to %s.", p.Origin, p.Destination), nil
	}

	route := result.Routes[0]
	leg := route.Legs[0]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Directions: %s → %s\n", leg.StartAddress, leg.EndAddress))
	sb.WriteString(fmt.Sprintf("Mode: %s | Distance: %s | Duration: %s\n", titleCase(p.Mode), leg.Distance.Text, leg.Duration.Text))
	if route.Summary != "" {
		sb.WriteString(fmt.Sprintf("Via: %s\n", route.Summary))
	}
	sb.WriteString("\nSteps:\n")
	for i, step := range leg.Steps {
		instruction := stripHTML(step.HTMLInstructions)
		sb.WriteString(fmt.Sprintf("  %d. %s (%s, %s)\n", i+1, instruction, step.Distance.Text, step.Duration.Text))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── maps.distance ─────────────────────────────────────────────────────────────

func mapsDistance(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Origin      string `json:"origin"`
		Destination string `json:"destination"`
		Mode        string `json:"mode"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Origin == "" || p.Destination == "" {
		return "", fmt.Errorf("origin and destination are required")
	}
	if p.Mode == "" {
		p.Mode = "driving"
	}

	bundle, _ := creds.Read()
	if bundle.GoogleMapsAPIKey == "" {
		return "", fmt.Errorf("Google Maps API key not configured — add it in Settings → Credentials")
	}

	u := googleMapsBase + "/distancematrix/json?" +
		"origins=" + url.QueryEscape(p.Origin) +
		"&destinations=" + url.QueryEscape(p.Destination) +
		"&mode=" + url.QueryEscape(p.Mode) +
		"&key=" + url.QueryEscape(bundle.GoogleMapsAPIKey)

	data, err := mapsHTTPGet(ctx, u, "")
	if err != nil {
		return "", fmt.Errorf("distance request failed: %w", err)
	}

	var result struct {
		Status               string   `json:"status"`
		OriginAddresses      []string `json:"origin_addresses"`
		DestinationAddresses []string `json:"destination_addresses"`
		Rows                 []struct {
			Elements []struct {
				Status   string                       `json:"status"`
				Distance struct{ Text string `json:"text"` } `json:"distance"`
				Duration struct{ Text string `json:"text"` } `json:"duration"`
			} `json:"elements"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("distance parse failed: %w", err)
	}
	if result.Status != "OK" {
		return "", fmt.Errorf("distance matrix error: %s", result.Status)
	}
	if len(result.Rows) == 0 || len(result.Rows[0].Elements) == 0 {
		return fmt.Sprintf("Could not calculate distance from %s to %s.", p.Origin, p.Destination), nil
	}

	elem := result.Rows[0].Elements[0]
	if elem.Status != "OK" {
		return fmt.Sprintf("Route not found from %s to %s.", p.Origin, p.Destination), nil
	}

	origin := p.Origin
	dest := p.Destination
	if len(result.OriginAddresses) > 0 && result.OriginAddresses[0] != "" {
		origin = result.OriginAddresses[0]
	}
	if len(result.DestinationAddresses) > 0 && result.DestinationAddresses[0] != "" {
		dest = result.DestinationAddresses[0]
	}

	return fmt.Sprintf("Distance (%s):\n  From: %s\n  To:   %s\n  Distance: %s\n  Duration: %s",
		titleCase(p.Mode), origin, dest, elem.Distance.Text, elem.Duration.Text), nil
}

// ── maps.my_location ──────────────────────────────────────────────────────────

func mapsMyLocation(ctx context.Context, _ json.RawMessage) (string, error) {
	// Try CoreLocation helper first — WiFi/GPS positioning, much more accurate.
	if result, err := coreLocationFetch(ctx); err == nil {
		return result, nil
	}

	// Fall back to IP-based geolocation.
	loc := location.Get()
	if location.ShouldRefresh() {
		if err := location.DetectFromIP(); err != nil {
			if loc.City == "" {
				return "", fmt.Errorf("location not available — set it manually in Settings → General, or try again: %w", err)
			}
			// Use stale cache rather than failing.
		} else {
			loc = location.Get()
		}
	}

	if loc.City == "" {
		return "Location not yet resolved. Set it in Settings → General or wait for auto-detection.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current location: %s, %s\n", loc.City, loc.Country))
	if loc.Latitude != 0 || loc.Longitude != 0 {
		sb.WriteString(fmt.Sprintf("Coordinates: %.6f, %.6f\n", loc.Latitude, loc.Longitude))
	}
	if loc.Timezone != "" {
		sb.WriteString(fmt.Sprintf("Timezone: %s\n", loc.Timezone))
	}
	sb.WriteString("Source: IP-based geolocation (city-level accuracy)")
	return sb.String(), nil
}

// coreLocationFetch runs the atlas-location helper binary (installed alongside
// the Atlas daemon) which uses macOS CoreLocation for WiFi/GPS positioning.
// Returns an error if the helper is not installed or the request fails — the
// caller falls back to IP geolocation in that case.
func coreLocationFetch(ctx context.Context) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	helperPath := filepath.Join(filepath.Dir(execPath), "atlas-location")
	if _, err := os.Stat(helperPath); os.IsNotExist(err) {
		return "", fmt.Errorf("atlas-location helper not installed")
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 13*time.Second)
	defer cancel()

	out, err := exec.CommandContext(fetchCtx, helperPath).Output()
	if err != nil {
		return "", fmt.Errorf("atlas-location: %w", err)
	}

	var gpsResult struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Accuracy  float64 `json:"accuracy"`
		Altitude  float64 `json:"altitude"`
		Error     string  `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &gpsResult); err != nil {
		return "", fmt.Errorf("atlas-location parse: %w", err)
	}
	if gpsResult.Error != "" {
		return "", fmt.Errorf("atlas-location: %s", gpsResult.Error)
	}

	// Reverse-geocode to get a human-readable address.
	var addr struct {
		DisplayName string `json:"display_name"`
		Address     struct {
			City    string `json:"city"`
			Town    string `json:"town"`
			Village string `json:"village"`
			Country string `json:"country"`
		} `json:"address"`
	}
	reverseURL := fmt.Sprintf("%s/reverse?lat=%.6f&lon=%.6f&format=json&addressdetails=1",
		nominatimBase, gpsResult.Latitude, gpsResult.Longitude)
	if data, geoErr := mapsHTTPGet(ctx, reverseURL, mapsUserAgent); geoErr == nil {
		_ = json.Unmarshal(data, &addr)
	}

	// Update the in-memory location cache with the precise coordinates so
	// weather and other location-aware skills benefit automatically.
	if addr.Address.Country != "" {
		city := addr.Address.City
		if city == "" {
			city = addr.Address.Town
		}
		if city == "" {
			city = addr.Address.Village
		}
		existing := location.Get()
		location.Set(location.Info{
			City:      city,
			Country:   addr.Address.Country,
			Timezone:  existing.Timezone,
			Latitude:  gpsResult.Latitude,
			Longitude: gpsResult.Longitude,
			Source:    "gps",
			UpdatedAt: time.Now(),
		})
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Coordinates: %.6f, %.6f\n", gpsResult.Latitude, gpsResult.Longitude))
	if gpsResult.Accuracy > 0 {
		sb.WriteString(fmt.Sprintf("Accuracy: ±%.0f meters\n", gpsResult.Accuracy))
	}
	if addr.DisplayName != "" {
		sb.WriteString(fmt.Sprintf("Address: %s\n", addr.DisplayName))
	}
	sb.WriteString("Source: CoreLocation (WiFi/GPS positioning)")
	return sb.String(), nil
}

// ── FnResult wrappers — concise LogOutcome for the activity log ───────────────
//
// These wrappers call the string-returning skill functions and set LogOutcome
// to a one-liner so the activity log doesn't show the full multi-line output.

func mapsGeocodeResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Address string `json:"address"`
	}
	_ = json.Unmarshal(args, &p)
	s, err := mapsGeocode(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: fmt.Sprintf("Geocoded: %q", p.Address),
	}, nil
}

func mapsReverseGeocodeResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	_ = json.Unmarshal(args, &p)
	s, err := mapsReverseGeocode(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: fmt.Sprintf("Reverse geocoded: (%.4f, %.4f)", p.Latitude, p.Longitude),
	}, nil
}

func mapsSearchResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query string `json:"query"`
		Max   int    `json:"max"`
	}
	_ = json.Unmarshal(args, &p)
	s, err := mapsSearch(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: fmt.Sprintf("Place search: %q", p.Query),
	}, nil
}

func mapsDirectionsResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Origin      string `json:"origin"`
		Destination string `json:"destination"`
		Mode        string `json:"mode"`
	}
	_ = json.Unmarshal(args, &p)
	if p.Mode == "" {
		p.Mode = "driving"
	}
	s, err := mapsDirections(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	// Extract distance + duration from the first line of the result for the log.
	logLine := fmt.Sprintf("Directions: %s → %s (%s)", p.Origin, p.Destination, p.Mode)
	for _, line := range strings.SplitN(s, "\n", 3) {
		if strings.HasPrefix(line, "Mode:") {
			logLine = fmt.Sprintf("Directions: %s → %s | %s", p.Origin, p.Destination,
				strings.TrimPrefix(line, "Mode: "))
			break
		}
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: logLine,
	}, nil
}

func mapsDistanceResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Origin      string `json:"origin"`
		Destination string `json:"destination"`
		Mode        string `json:"mode"`
	}
	_ = json.Unmarshal(args, &p)
	if p.Mode == "" {
		p.Mode = "driving"
	}
	s, err := mapsDistance(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	// Extract the distance/duration values from the result lines for the log.
	logLine := fmt.Sprintf("Distance: %s → %s (%s)", p.Origin, p.Destination, p.Mode)
	var distText, durText string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Distance:") {
			distText = strings.TrimPrefix(line, "Distance: ")
		}
		if strings.HasPrefix(line, "Duration:") {
			durText = strings.TrimPrefix(line, "Duration: ")
		}
	}
	if distText != "" && durText != "" {
		logLine = fmt.Sprintf("Distance: %s → %s: %s, %s (%s)",
			p.Origin, p.Destination, distText, durText, p.Mode)
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: logLine,
	}, nil
}

func mapsMyLocationResult(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	s, err := mapsMyLocation(ctx, args)
	if err != nil {
		return ToolResult{Success: false, Summary: err.Error()}, err
	}
	// Extract city/source for a compact log line.
	logLine := "Location fetched"
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "Current location:") || strings.HasPrefix(line, "Coordinates:") {
			logLine = strings.TrimSpace(line)
			break
		}
	}
	return ToolResult{
		Success:    true,
		Summary:    s,
		LogOutcome: logLine,
	}, nil
}
