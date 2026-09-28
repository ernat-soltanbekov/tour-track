// Package catalog fetches and joins the four API resources and caches complete
// snapshots. Request-time classification deliberately lives outside the cache.
package catalog

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ernat-soltanbekov/tour-track/internal/activity"
)

type Artist struct {
	ID           int      `json:"id"`
	Image        string   `json:"image"`
	Name         string   `json:"name"`
	Members      []string `json:"members"`
	CreationDate int      `json:"creationDate"`
	FirstAlbum   string   `json:"firstAlbum"`
	Locations    []string `json:"locations"`
	Dates        []string `json:"dates"`
	Concerts     []Stop   `json:"concerts"`
}

type Stop struct {
	Location string   `json:"location"`
	Display  string   `json:"display"`
	Country  string   `json:"country"`
	Dates    []string `json:"dates"`
}

type View struct {
	Artist
	Countries      []string `json:"countries"`
	TourSpread     int      `json:"tourSpread"`
	ConcertCount   int      `json:"concertCount"`
	CareerYears    int      `json:"careerYears"`
	CareerPoints   int      `json:"careerPoints"`
	Score          int      `json:"score"`
	Label          string   `json:"label"`
	RecentConcerts int      `json:"recentConcerts"`
	RisingStar     bool     `json:"risingStar"`
	Year           int      `json:"year"`
	UnlinkedDates  []string `json:"unlinkedDates"`
}

// Enrich is called for every HTTP request, including when raw data is cached.
func Enrich(artist Artist, now time.Time) View {
	countries := make(map[string]bool)
	for _, location := range artist.Locations {
		if country := Country(location); country != "" {
			countries[country] = true
		}
	}
	v := View{Artist: artist, Countries: []string{}, UnlinkedDates: []string{}, Year: now.Year()}
	for country := range countries {
		v.Countries = append(v.Countries, country)
	}
	sort.Strings(v.Countries)
	v.TourSpread = len(v.Countries)
	v.ConcertCount = len(artist.Dates) // Dates endpoint is authoritative for the score.
	v.CareerYears = max(0, now.Year()-artist.CreationDate)
	v.CareerPoints = min(10, v.CareerYears/5)
	v.Score = activity.Score(v.TourSpread, v.ConcertCount, v.CareerYears)
	v.Label = activity.Classify(v.TourSpread, v.ConcertCount, v.CareerYears)
	v.RecentConcerts = activity.RecentCount(artist.Dates, now)
	v.RisingStar = activity.IsRisingStar(v.RecentConcerts, v.ConcertCount, v.CareerYears)
	// Some real API entries have dates absent from relation. Keep these visible
	// instead of inventing a venue or silently discarding them.
	linked := make(map[string]int)
	for _, stop := range artist.Concerts {
		for _, date := range stop.Dates {
			linked[date]++
		}
	}
	for _, date := range artist.Dates {
		if linked[date] > 0 {
			linked[date]--
		} else {
			v.UnlinkedDates = append(v.UnlinkedDates, date)
		}
	}
	return v
}

// Country splits at the final dash, preserving dashes inside city names.
func Country(location string) string {
	_, country, ok := strings.Cut(strings.ToLower(strings.TrimSpace(location)), "-")
	if !ok {
		return ""
	}
	if index := strings.LastIndex(country, "-"); index >= 0 {
		country = country[index+1:]
	}
	country = strings.TrimSpace(country)
	switch country {
	case "us", "united_states", "united_states_of_america":
		return "usa"
	case "united_kingdom", "great_britain":
		return "uk"
	case "brasil":
		return "brazil"
	}
	return country
}

func DisplayLocation(location string) string {
	return strings.ReplaceAll(strings.ReplaceAll(location, "_", " "), "-", ", ")
}

func validateArtist(a Artist) error {
	if a.ID <= 0 || strings.TrimSpace(a.Name) == "" || len(a.Name) > 300 || a.CreationDate < 1 || a.CreationDate > 9999 {
		return fmt.Errorf("invalid artist identity or creation year")
	}
	if _, err := activity.ParseDate(a.FirstAlbum); err != nil {
		return fmt.Errorf("invalid first album for artist %d", a.ID)
	}
	return nil
}

// Only the known API image origin is allowed. All other images use a local
// fallback; upstream content must never become executable URLs in the browser.
func safeImage(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "groupietrackers.herokuapp.com" || u.User != nil || !strings.HasPrefix(u.Path, "/api/images/") {
		return "/static/artist.svg"
	}
	return u.String()
}
