package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This is a recorded API response, not production fallback data. Tests never
// need the public API, so outages cannot make the audit nondeterministic.
func fixtureHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.FileServer(http.Dir("testdata"))
}

func fixtureServer(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	files := fixtureHandler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		r.URL.Path += ".json"
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLoadAllFourAndAuditRecords(t *testing.T) {
	var count atomic.Int64
	server := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) bool { count.Add(1); return false })
	client, _ := NewClient(server.URL, time.Second)
	artists, err := client.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 52 || count.Load() != 4 {
		t.Fatalf("artists=%d requests=%d", len(artists), count.Load())
	}
	byName := map[string]Artist{}
	for _, a := range artists {
		byName[a.Name] = a
	}
	wantQueen := []string{"Freddie Mercury", "Brian May", "John Daecon", "Roger Meddows-Taylor", "Mike Grose", "Barry Mitchell", "Doug Fogie"}
	if !reflect.DeepEqual(byName["Queen"].Members, wantQueen) {
		t.Fatal("Queen members differ from subject")
	}
	if byName["Gorillaz"].FirstAlbum != "26-03-2001" {
		t.Fatal("Gorillaz first album differs")
	}
	wantFoo := []string{"Dave Grohl", "Nate Mendel", "Taylor Hawkins", "Chris Shiflett", "Pat Smear", "Rami Jaffee"}
	if !reflect.DeepEqual(byName["Foo Fighters"].Members, wantFoo) {
		t.Fatal("Foo Fighters members differ")
	}
	wantTravis := []string{"santiago-chile", "sao_paulo-brasil", "los_angeles-usa", "houston-usa", "atlanta-usa", "new_orleans-usa", "philadelphia-usa", "london-uk", "frauenfeld-switzerland", "turku-finland"}
	gotTravis := append([]string(nil), byName["Travis Scott"].Locations...)
	for i := range wantTravis {
		wantTravis[i] = strings.ReplaceAll(wantTravis[i], "-brasil", "-brazil")
		gotTravis[i] = strings.ReplaceAll(gotTravis[i], "-brasil", "-brazil")
	}
	if !reflect.DeepEqual(gotTravis, wantTravis) {
		t.Fatal("Travis locations differ")
	}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	travis := Enrich(byName["Travis Scott"], now)
	if travis.Label != "Legendary" || travis.TourSpread != 6 || travis.ConcertCount != 12 || travis.Score != 51 {
		t.Fatalf("Travis: %+v", travis)
	}
	foo := Enrich(byName["Foo Fighters"], now)
	if foo.ConcertCount != 8 || len(foo.UnlinkedDates) != 1 {
		t.Fatalf("unlinked date lost: %+v", foo)
	}
	if got := byName["Queen"].Concerts[0]; got.Location != "dunedin-new_zealand" || got.Dates[0] != "10-02-2020" {
		t.Fatalf("bad join: %+v", got)
	}
}

func TestClientFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"upstream 500", "", 500}, {"upstream rate limit", "", 429}, {"redirect", "", 302},
		{"invalid JSON", "{", 200}, {"trailing JSON", "[] {}", 200}, {"wrong schema", "{}", 200}, {"empty", "[]", 200},
		{"null", "null", 200}, {"oversized", strings.Repeat(" ", MaxResponseBytes+1), 200},
		{"duplicate artist", `[{"id":1,"name":"A","creationDate":2000,"firstAlbum":"01-01-2000"},{"id":1,"name":"B","creationDate":2000,"firstAlbum":"01-01-2000"}]`, 200},
		{"invalid artist", `[{"id":-1,"name":"","creationDate":0}]`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/artists" {
					return false
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
				return true
			})
			client, _ := NewClient(server.URL, time.Second)
			if artists, err := client.Load(context.Background()); err == nil || artists != nil {
				t.Fatal("bad upstream data accepted")
			}
		})
	}
}

func TestJoinRejectsBrokenIDsAndDates(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"missing location", "/locations", `{"index":[]}`},
		{"unknown id", "/locations", `{"index":[{"id":999}]}`},
		{"duplicate location", "/locations", `{"index":[{"id":1},{"id":1}]}`},
		{"invalid location", "/locations", `{"index":[{"id":1,"locations":["not_a_location"]}]}`},
		{"invalid date", "/dates", `{"index":[{"id":1,"dates":["31-02-2020"]}]}`},
		{"missing dates", "/dates", `{"index":[]}`},
		{"unknown dates id", "/dates", `{"index":[{"id":999}]}`},
		{"missing relation", "/relation", `{"index":[]}`},
		{"unknown relation id", "/relation", `{"index":[{"id":999}]}`},
		{"invalid relation date", "/relation", `{"index":[{"id":1,"datesLocations":{"city-usa":["invalid"]}}]}`},
		{"invalid relation location", "/relation", `{"index":[{"id":1,"datesLocations":{"invalid":[]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != tc.path {
					return false
				}
				_, _ = fmt.Fprint(w, tc.body)
				return true
			})
			client, _ := NewClient(server.URL, time.Second)
			if _, err := client.Load(context.Background()); err == nil {
				t.Fatal("invalid join accepted")
			}
		})
	}
}

func TestClientTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, _ := NewClient(server.URL, 25*time.Millisecond)
	start := time.Now()
	if _, err := client.Load(context.Background()); err == nil {
		t.Fatal("timeout missing")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout was unbounded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Load(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestParallelFetch(t *testing.T) {
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	server := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return false
	})
	client, _ := NewClient(server.URL, time.Second)
	done := make(chan error, 1)
	go func() { _, err := client.Load(context.Background()); done <- err }()
	for range 4 {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("requests were not concurrent")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationAndImageSafety(t *testing.T) {
	for _, base := range []string{"", ":bad", "ftp://example.org", "https://user:pass@example.org", "https://example.org?q=1", "https://example.org/#x"} {
		if _, err := NewClient(base, time.Second); err == nil {
			t.Errorf("accepted %s", base)
		}
	}
	if _, err := NewClient("https://example.org", 0); err == nil {
		t.Fatal("zero timeout accepted")
	}
	for _, raw := range []string{"javascript:alert(1)", "https://evil.test/a.png", "//evil.test/x", "https://groupietrackers.herokuapp.com.evil.test/api/images/x", "https://groupietrackers.herokuapp.com/x"} {
		if safeImage(raw) != "/static/artist.svg" {
			t.Errorf("unsafe image %q", raw)
		}
	}
	valid := "https://groupietrackers.herokuapp.com/api/images/queen.jpeg"
	if safeImage(valid) != valid {
		t.Fatal("valid image rejected")
	}
}

func TestJoinUsesIDsNotArrayPositions(t *testing.T) {
	data, err := os.ReadFile("testdata/locations.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Index []json.RawMessage `json:"index"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(raw.Index)-1; i < j; i, j = i+1, j-1 {
		raw.Index[i], raw.Index[j] = raw.Index[j], raw.Index[i]
	}
	encoded, _ := json.Marshal(raw)
	server := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/locations" {
			return false
		}
		_, _ = w.Write(encoded)
		return true
	})
	client, _ := NewClient(server.URL, time.Second)
	artists, err := client.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if artists[0].Name != "Queen" || artists[0].Locations[0] != "north_carolina-usa" {
		t.Fatal("records linked by position")
	}
}

func TestCountryAndRequestYear(t *testing.T) {
	for raw, want := range map[string]string{"sao_paulo-brasil": "brazil", "city-usa": "usa", "city-united_states": "usa", "city-with-dashes-new_zealand": "new_zealand", " LONDON-UK ": "uk", "invalid": "", "city-": ""} {
		if got := Country(raw); got != want {
			t.Errorf("Country(%q)=%q want %q", raw, got, want)
		}
	}
	a := Artist{CreationDate: 2000, Locations: []string{"city-usa", "other-usa", "third-us", "x-uk"}, Dates: []string{"01-01-2024"}}
	before := Enrich(a, time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC))
	after := Enrich(a, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if after.CareerYears != 25 || before.CareerYears != 24 || after.Score != before.Score+1 || after.TourSpread != 2 {
		t.Fatal("request year or country deduplication wrong")
	}
	future := Enrich(Artist{CreationDate: 2099}, time.Now())
	if future.CareerYears != 0 || future.Label != "Emerging" {
		t.Fatal("negative career not handled")
	}
}

func FuzzCountry(f *testing.F) {
	f.Add("city-usa")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		got := Country(raw)
		if Country(raw) != got {
			t.Fatal("not deterministic")
		}
	})
}
