package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ernat-soltanbekov/tour-track/internal/activity"
)

const MaxResponseBytes = 4 << 20

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || timeout <= 0 {
		return nil, fmt.Errorf("API_BASE_URL must be an http(s) URL without credentials, query, or fragment; timeout must be positive")
	}
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Load fetches fixed endpoints concurrently. API-provided links are never
// followed. A complete, validated snapshot replaces the cache in one step.
func (c *Client) Load(ctx context.Context) ([]Artist, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var artists []struct {
		ID           int      `json:"id"`
		Name         string   `json:"name"`
		Image        string   `json:"image"`
		Members      []string `json:"members"`
		CreationDate int      `json:"creationDate"`
		FirstAlbum   string   `json:"firstAlbum"`
	}
	var locations struct {
		Index []struct {
			ID        int      `json:"id"`
			Locations []string `json:"locations"`
		} `json:"index"`
	}
	var dates struct {
		Index []struct {
			ID    int      `json:"id"`
			Dates []string `json:"dates"`
		} `json:"index"`
	}
	var relations struct {
		Index []struct {
			ID             int                 `json:"id"`
			DatesLocations map[string][]string `json:"datesLocations"`
		} `json:"index"`
	}
	tasks := []struct {
		path   string
		target any
	}{{"artists", &artists}, {"locations", &locations}, {"dates", &dates}, {"relation", &relations}}
	results := make(chan error, len(tasks))
	for _, task := range tasks {
		go func(path string, target any) { results <- c.fetch(ctx, path, target) }(task.path, task.target)
	}
	var firstError error
	for range tasks {
		if err := <-results; err != nil && firstError == nil {
			firstError = err
			cancel()
		}
	}
	if firstError != nil {
		return nil, firstError
	}
	if len(artists) == 0 || len(artists) > 10000 {
		return nil, fmt.Errorf("invalid artist count")
	}
	byID := make(map[int]*Artist, len(artists))
	joined := make([]Artist, len(artists))
	for i, raw := range artists {
		a := Artist{ID: raw.ID, Name: raw.Name, Image: safeImage(raw.Image), Members: raw.Members, CreationDate: raw.CreationDate, FirstAlbum: raw.FirstAlbum, Locations: []string{}, Dates: []string{}, Concerts: []Stop{}}
		if err := validateArtist(a); err != nil {
			return nil, err
		}
		if byID[a.ID] != nil {
			return nil, fmt.Errorf("duplicate artist ID %d", a.ID)
		}
		if a.Members == nil {
			a.Members = []string{}
		}
		joined[i] = a
		byID[a.ID] = &joined[i]
	}
	seen := make(map[int]bool)
	for _, row := range locations.Index {
		a, err := match(byID, seen, row.ID, "locations")
		if err != nil {
			return nil, err
		}
		for _, location := range row.Locations {
			if Country(location) == "" || len(location) > 300 {
				return nil, fmt.Errorf("invalid location for artist %d", row.ID)
			}
			a.Locations = append(a.Locations, location)
		}
	}
	if len(seen) != len(byID) {
		return nil, fmt.Errorf("missing locations record")
	}
	seen = make(map[int]bool)
	for _, row := range dates.Index {
		a, err := match(byID, seen, row.ID, "dates")
		if err != nil {
			return nil, err
		}
		for _, raw := range row.Dates {
			date, err := activity.ParseDate(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid concert date for artist %d", row.ID)
			}
			a.Dates = append(a.Dates, date.Format("02-01-2006"))
		}
	}
	if len(seen) != len(byID) {
		return nil, fmt.Errorf("missing dates record")
	}
	seen = make(map[int]bool)
	for _, row := range relations.Index {
		a, err := match(byID, seen, row.ID, "relation")
		if err != nil {
			return nil, err
		}
		for location, rawDates := range row.DatesLocations {
			if Country(location) == "" || len(location) > 300 {
				return nil, fmt.Errorf("invalid relation location")
			}
			stop := Stop{Location: location, Display: DisplayLocation(location), Country: Country(location), Dates: []string{}}
			for _, raw := range rawDates {
				date, err := activity.ParseDate(raw)
				if err != nil {
					return nil, fmt.Errorf("invalid relation date")
				}
				stop.Dates = append(stop.Dates, date.Format("02-01-2006"))
			}
			a.Concerts = append(a.Concerts, stop)
		}
		sort.Slice(a.Concerts, func(i, j int) bool { return a.Concerts[i].Location < a.Concerts[j].Location })
	}
	if len(seen) != len(byID) {
		return nil, fmt.Errorf("missing relation record")
	}
	sort.Slice(joined, func(i, j int) bool { return joined[i].ID < joined[j].ID })
	return joined, nil
}

func match(artists map[int]*Artist, seen map[int]bool, id int, resource string) (*Artist, error) {
	if artists[id] == nil || seen[id] {
		return nil, fmt.Errorf("unknown or duplicate ID %d in %s", id, resource)
	}
	seen[id] = true
	return artists[id], nil
}

func (c *Client) fetch(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: HTTP %d", path, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > MaxResponseBytes {
		return fmt.Errorf("%s exceeds response size limit", path)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
