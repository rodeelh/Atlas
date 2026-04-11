package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"atlas-runtime-go/internal/location"
	"atlas-runtime-go/internal/preferences"
)

type ProfileService struct{}

func NewProfileService() *ProfileService { return &ProfileService{} }

type LocationResponse struct {
	City      string  `json:"city"`
	Country   string  `json:"country"`
	Timezone  string  `json:"timezone"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Source    string  `json:"source"`
	UpdatedAt string  `json:"updatedAt"`
}

type PreferencesResponse struct {
	TemperatureUnit string `json:"temperatureUnit"`
	Currency        string `json:"currency"`
	UnitSystem      string `json:"unitSystem"`
}

type LinkPreviewResult struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"imageURL,omitempty"`
}

func (s *ProfileService) GetLocation() LocationResponse {
	return locationToResponse(location.Get())
}

func (s *ProfileService) SetLocation(city, country string) (LocationResponse, error) {
	if err := location.SetManual(strings.TrimSpace(city), strings.TrimSpace(country)); err != nil {
		return LocationResponse{}, err
	}
	return locationToResponse(location.Get()), nil
}

func (s *ProfileService) DetectLocation() (LocationResponse, error) {
	if err := location.DetectFromIP(); err != nil {
		return LocationResponse{}, err
	}
	return locationToResponse(location.Get()), nil
}

func (s *ProfileService) GetPreferences() PreferencesResponse {
	p := preferences.Get()
	return PreferencesResponse{
		TemperatureUnit: p.TemperatureUnit,
		Currency:        p.Currency,
		UnitSystem:      p.UnitSystem,
	}
}

func (s *ProfileService) UpdatePreferences(tempUnit, currency, unitSystem string) PreferencesResponse {
	p := preferences.Get()
	if tempUnit != "" {
		p.TemperatureUnit = tempUnit
	}
	if currency != "" {
		p.Currency = strings.ToUpper(currency)
	}
	if unitSystem != "" {
		p.UnitSystem = unitSystem
	}
	preferences.Set(p)
	return PreferencesResponse{
		TemperatureUnit: p.TemperatureUnit,
		Currency:        p.Currency,
		UnitSystem:      p.UnitSystem,
	}
}

// SetLocationFromCoords reverse-geocodes browser-supplied GPS coordinates
// via Nominatim, then stores the result exactly like DetectFromCoreLocation.
func (s *ProfileService) SetLocationFromCoords(lat, lon float64) (LocationResponse, error) {
	reverseURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/reverse?lat=%.6f&lon=%.6f&format=json&addressdetails=1",
		lat, lon,
	)
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest("GET", reverseURL, nil)
	if err != nil {
		return LocationResponse{}, fmt.Errorf("reverse geocode request: %w", err)
	}
	req.Header.Set("User-Agent", "ProjectAtlas/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return LocationResponse{}, fmt.Errorf("reverse geocode: %w", err)
	}
	defer resp.Body.Close()

	var geo struct {
		Address struct {
			City        string `json:"city"`
			Town        string `json:"town"`
			Village     string `json:"village"`
			State       string `json:"state"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
		} `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil || geo.Address.Country == "" {
		return LocationResponse{}, fmt.Errorf("reverse geocode parse failed")
	}

	city := geo.Address.City
	if city == "" {
		city = geo.Address.Town
	}
	if city == "" {
		city = geo.Address.Village
	}
	if city == "" {
		city = geo.Address.State
	}

	if err := location.SetManual(city, geo.Address.Country); err != nil {
		return LocationResponse{}, err
	}
	// Override source to "gps" since coordinates came from the browser's GPS.
	loc := location.Get()
	loc.Source = "gps"
	loc.Latitude = lat
	loc.Longitude = lon
	location.Set(loc)
	return locationToResponse(loc), nil
}

func (s *ProfileService) FetchLinkPreview(rawURL string) (LinkPreviewResult, error) {
	result := LinkPreviewResult{URL: rawURL}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("User-Agent", "Atlas/1.0 link-preview")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	httpResp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer httpResp.Body.Close()
	body := make([]byte, 256*1024)
	n, _ := httpResp.Body.Read(body)
	html := string(body[:n])
	result.Title = extractHTMLMeta(html, "og:title")
	if result.Title == "" {
		result.Title = extractHTMLTitle(html)
	}
	result.Description = extractHTMLMeta(html, "og:description")
	if result.Description == "" {
		result.Description = extractHTMLMeta(html, "description")
	}
	result.ImageURL = extractHTMLMeta(html, "og:image")
	return result, nil
}

func locationToResponse(loc location.Info) LocationResponse {
	updatedAt := ""
	if !loc.UpdatedAt.IsZero() {
		updatedAt = loc.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return LocationResponse{
		City:      loc.City,
		Country:   loc.Country,
		Timezone:  loc.Timezone,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Source:    loc.Source,
		UpdatedAt: updatedAt,
	}
}

func extractHTMLMeta(html, name string) string {
	lower := strings.ToLower(html)
	nameAttr := `property="` + name + `"`
	if !strings.Contains(lower, strings.ToLower(nameAttr)) {
		nameAttr = `name="` + name + `"`
	}
	idx := strings.Index(lower, strings.ToLower(nameAttr))
	if idx < 0 {
		return ""
	}
	rest := html[idx:]
	ci := strings.Index(strings.ToLower(rest), `content="`)
	if ci < 0 {
		return ""
	}
	rest = rest[ci+9:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func extractHTMLTitle(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title>")
	if start < 0 {
		return ""
	}
	start += 7
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html[start : start+end])
}
