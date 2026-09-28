// Package web provides server-rendered pages and a JSON API. Both use the same
// request-time scoring and filtering, so the website also works without JS.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ernat-soltanbekov/tour-track/internal/catalog"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Source interface {
	Get(context.Context) (catalog.Snapshot, error)
}

type Server struct {
	source    Source
	templates *template.Template
	now       func() time.Time
}

type Filters struct{ Query, Label, Country, Sort string }

type Listing struct {
	Artists        []catalog.View `json:"artists"`
	Total          int            `json:"total"`
	Matched        int            `json:"matched"`
	Countries      []string       `json:"countries"`
	ConcertCount   int            `json:"concertCount"`
	LegendaryCount int            `json:"legendaryCount"`
	FetchedAt      time.Time      `json:"fetchedAt"`
	Stale          bool           `json:"stale"`
	Year           int            `json:"year"`
}

type page struct {
	Title  string
	Active string
	Listing
	Filters Filters
	Artist  catalog.View
	Status  int
	Message string
}

func New(source Source) (http.Handler, error) {
	if source == nil {
		return nil, fmt.Errorf("catalog source is required")
	}
	views, err := template.New("").Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"place": catalog.DisplayLocation,
		"date":  func(t time.Time) string { return t.UTC().Format("02 Jan 2006, 15:04 UTC") },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{source: source, templates: views, now: time.Now}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(static))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.RequestURI) > 4096 {
			s.failure(w, r, http.StatusRequestURITooLong, "The request address is too long.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.failure(w, r, http.StatusMethodNotAllowed, "Use GET or HEAD for this resource.")
			return
		}
		switch {
		case r.URL.Path == "/":
			s.list(w, r)
		case r.URL.Path == "/about":
			s.render(w, "about", page{Title: "The field notes", Active: "about"}, http.StatusOK)
		case r.URL.Path == "/api/artists":
			s.list(w, r)
		case strings.HasPrefix(r.URL.Path, "/artists/"):
			s.detail(w, r, "/artists/")
		case strings.HasPrefix(r.URL.Path, "/api/artists/"):
			s.detail(w, r, "/api/artists/")
		case r.URL.Path == "/healthz":
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case r.URL.Path == "/readyz":
			snapshot, err := s.source.Get(r.Context())
			if err != nil {
				s.failure(w, r, http.StatusServiceUnavailable, "Catalog is temporarily unavailable.")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "stale": snapshot.Stale, "fetchedAt": snapshot.FetchedAt})
		case strings.HasPrefix(r.URL.Path, "/static/"):
			name := strings.TrimPrefix(r.URL.Path, "/static/")
			if strings.Contains(name, "/") || name == "." {
				s.failure(w, r, http.StatusNotFound, "This page does not exist.")
				return
			}
			if _, err := fs.Stat(static, name); err != nil {
				s.failure(w, r, http.StatusNotFound, "This file does not exist.")
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.StripPrefix("/static/", files).ServeHTTP(w, r)
		default:
			s.failure(w, r, http.StatusNotFound, "This page does not exist. Return to the artist directory.")
		}
	})
	return protect(handler, 64), nil
}

func parseFilters(values url.Values) (Filters, error) {
	f := Filters{Query: strings.TrimSpace(values.Get("q")), Label: values.Get("label"), Country: values.Get("country"), Sort: values.Get("sort")}
	for key, all := range values {
		if len(all) != 1 || (key != "q" && key != "label" && key != "country" && key != "sort") {
			return f, fmt.Errorf("Use only one q, label, country, or sort parameter.")
		}
	}
	if !utf8.ValidString(f.Query) || utf8.RuneCountInString(f.Query) > 200 || len(f.Country) > 80 {
		return f, fmt.Errorf("Search is limited to 200 characters.")
	}
	if f.Label != "" && f.Label != "Emerging" && f.Label != "Active" && f.Label != "Established" && f.Label != "Legendary" && f.Label != "Rising Star" {
		return f, fmt.Errorf("Unknown activity label.")
	}
	if f.Sort == "" {
		f.Sort = "name"
	}
	if f.Sort != "name" && f.Sort != "score" && f.Sort != "newest" {
		return f, fmt.Errorf("Unknown sort order.")
	}
	return f, nil
}

func makeListing(snapshot catalog.Snapshot, filters Filters, now time.Time) Listing {
	list := Listing{Artists: []catalog.View{}, Total: len(snapshot.Artists), Countries: []string{}, FetchedAt: snapshot.FetchedAt, Stale: snapshot.Stale, Year: now.Year()}
	countries := make(map[string]bool)
	needle := strings.ToLower(filters.Query)
	for _, raw := range snapshot.Artists {
		a := catalog.Enrich(raw, now)
		list.ConcertCount += a.ConcertCount
		if a.Label == "Legendary" {
			list.LegendaryCount++
		}
		for _, country := range a.Countries {
			countries[country] = true
		}
		if filters.Label == "Rising Star" && !a.RisingStar {
			continue
		}
		if filters.Label != "" && filters.Label != "Rising Star" && filters.Label != a.Label {
			continue
		}
		if filters.Country != "" && !contains(a.Countries, filters.Country) {
			continue
		}
		haystack := strings.ToLower(a.Name + " " + strings.Join(a.Members, " ") + " " + strings.Join(a.Locations, " ") + " " + strings.Join(a.Countries, " "))
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		list.Artists = append(list.Artists, a)
	}
	for country := range countries {
		list.Countries = append(list.Countries, country)
	}
	sort.Strings(list.Countries)
	sort.SliceStable(list.Artists, func(i, j int) bool {
		a, b := list.Artists[i], list.Artists[j]
		if filters.Sort == "score" && a.Score != b.Score {
			return a.Score > b.Score
		}
		if filters.Sort == "newest" && a.CreationDate != b.CreationDate {
			return a.CreationDate > b.CreationDate
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	list.Matched = len(list.Artists)
	return list
}

func contains(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.failure(w, r, http.StatusBadRequest, "Malformed query parameters.")
		return
	}
	filters, err := parseFilters(values)
	if err != nil {
		s.failure(w, r, http.StatusBadRequest, err.Error())
		return
	}
	snapshot, err := s.source.Get(r.Context())
	if err != nil {
		s.failure(w, r, http.StatusServiceUnavailable, "The music archive is temporarily unavailable. Please try again shortly.")
		return
	}
	list := makeListing(snapshot, filters, s.now())
	if r.URL.Path == "/api/artists" {
		writeJSON(w, http.StatusOK, list)
		return
	}
	s.render(w, "index", page{Title: "Music in motion", Active: "artists", Listing: list, Filters: filters}, http.StatusOK)
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request, prefix string) {
	rawID := strings.TrimPrefix(r.URL.Path, prefix)
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 || strconv.Itoa(id) != rawID {
		s.failure(w, r, http.StatusNotFound, "Artist not found.")
		return
	}
	snapshot, err := s.source.Get(r.Context())
	if err != nil {
		s.failure(w, r, http.StatusServiceUnavailable, "The music archive is temporarily unavailable. Please try again shortly.")
		return
	}
	for _, raw := range snapshot.Artists {
		if raw.ID != id {
			continue
		}
		a := catalog.Enrich(raw, s.now())
		if strings.HasPrefix(prefix, "/api/") {
			writeJSON(w, http.StatusOK, map[string]any{"artist": a, "fetchedAt": snapshot.FetchedAt, "stale": snapshot.Stale})
			return
		}
		s.render(w, "detail", page{Title: a.Name, Active: "artists", Artist: a, Listing: Listing{FetchedAt: snapshot.FetchedAt, Stale: snapshot.Stale, Year: a.Year}}, http.StatusOK)
		return
	}
	s.failure(w, r, http.StatusNotFound, "Artist not found. Browse the directory to find another artist.")
}

func (s *Server) failure(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "30")
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/readyz" {
		writeJSON(w, status, map[string]any{"error": message, "status": status})
		return
	}
	s.render(w, "error", page{Title: http.StatusText(status), Status: status, Message: message}, status)
}

func (s *Server) render(w http.ResponseWriter, name string, data page, status int) {
	var body bytes.Buffer
	if err := s.templates.ExecuteTemplate(&body, name, data); err != nil {
		panic(fmt.Errorf("render %s: %w", name, err))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("encode response", "error", err)
	}
}
