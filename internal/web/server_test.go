package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ernat-soltanbekov/tour-track/internal/catalog"
)

type sourceFunc func(context.Context) (catalog.Snapshot, error)

func (f sourceFunc) Get(ctx context.Context) (catalog.Snapshot, error) { return f(ctx) }

func auditSource(t *testing.T) Source {
	t.Helper()
	files := http.FileServer(http.Dir("../catalog/testdata"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.URL.Path += ".json"; files.ServeHTTP(w, r) }))
	defer upstream.Close()
	client, _ := catalog.NewClient(upstream.URL, time.Second)
	artists, err := client.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sourceFunc(func(context.Context) (catalog.Snapshot, error) {
		return catalog.Snapshot{Artists: artists, FetchedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}, nil
	})
}

func newAuditHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := New(auditSource(t))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(h http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(method, path, nil))
	return r
}

func TestAllArtistPagesAndBadges(t *testing.T) {
	h := newAuditHandler(t)
	list := request(h, "GET", "/")
	if list.Code != 200 || strings.Count(list.Body.String(), `class="artist-card"`) != 52 {
		t.Fatal("listing incomplete", list.Code)
	}
	for id := 1; id <= 52; id++ {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			response := request(h, "GET", fmt.Sprintf("/artists/%d", id))
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, "Every point accounted for.") || !strings.Contains(body, `class="badge `) {
				t.Fatalf("detail invalid: %d", response.Code)
			}
		})
	}
	for path, text := range map[string]string{"/artists/1": "Roger Meddows-Taylor", "/artists/39": "26-03-2001", "/artists/30": "frauenfeld-switzerland", "/artists/51": "Rami Jaffee"} {
		if !strings.Contains(request(h, "GET", path).Body.String(), text) {
			t.Errorf("audit record missing: %s", path)
		}
	}
}

func TestRoutesAndHTTPMethods(t *testing.T) {
	h := newAuditHandler(t)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/", 200}, {"GET", "/about", 200}, {"GET", "/healthz", 200}, {"GET", "/readyz", 200}, {"GET", "/api/artists", 200}, {"GET", "/api/artists/1", 200},
		{"GET", "/static/style.css", 200}, {"GET", "/static/app.js", 200}, {"GET", "/static/favicon.svg", 200}, {"GET", "/static/artist.svg", 200},
		{"GET", "/static/", 404}, {"GET", "/static/missing.css", 404}, {"GET", "/static/../main.go", 404}, {"GET", "/robots.txt", 404}, {"GET", "/artists/nope", 404},
		{"GET", "/artists/01", 404}, {"GET", "/artists/-1", 404}, {"GET", "/artists/99999", 404}, {"GET", "/artists/1/extra", 404},
		{"GET", "/api/artists/99999", 404}, {"POST", "/", 405}, {"DELETE", "/api/artists", 405}, {"PUT", "/artists/1", 405}, {"OPTIONS", "/", 405},
		{"GET", "/?label=Fake", 400}, {"GET", "/?sort=bad", 400}, {"GET", "/?q=a&q=b", 400}, {"GET", "/?surprise=true", 400}, {"GET", "/?q=%zz", 400},
		{"GET", "/?q=" + strings.Repeat("a", 201), 400}, {"GET", "/?q=" + strings.Repeat("a", 5000), 414},
	} {
		t.Run(tc.method+tc.path[:min(70, len(tc.path))], func(t *testing.T) {
			r := request(h, tc.method, tc.path)
			if r.Code != tc.status {
				t.Fatalf("got %d want %d: %s", r.Code, tc.status, r.Body.String())
			}
			if r.Header().Get("Content-Security-Policy") == "" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security headers")
			}
			if tc.status == 405 && r.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("Allow header missing")
			}
		})
	}
	for _, path := range []string{"/", "/about", "/artists/1", "/api/artists", "/static/style.css", "/missing"} {
		r := request(h, "HEAD", path)
		if r.Body.Len() != 0 {
			t.Errorf("HEAD body: %s", path)
		}
	}
}

func TestSearchFiltersAndSort(t *testing.T) {
	h := newAuditHandler(t)
	for _, tc := range []struct {
		query string
		want  int
		name  string
	}{
		{"q=queen", 2, ""}, {"q=Freddie+Mercury", 1, "Queen"}, {"q=frauenfeld", 1, "Travis Scott"}, {"q=there_is_no_such_artist_12345", 0, ""},
		{"q=%20GoRiLlAz%20", 1, "Gorillaz"}, {"q=%D0%90%D1%81%D1%82%D0%B0%D0%BD%D0%B0", 0, ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			r := request(h, "GET", "/api/artists?"+tc.query)
			var data Listing
			if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if r.Code != 200 || data.Matched != tc.want {
				t.Fatalf("matched=%d want=%d", data.Matched, tc.want)
			}
			if tc.name != "" && data.Artists[0].Name != tc.name {
				t.Fatal("wrong artist")
			}
		})
	}
	for _, query := range []string{"sort=score", "sort=newest", "label=Legendary", "country=uk", "label=Rising+Star"} {
		r := request(h, "GET", "/api/artists?"+query)
		var data Listing
		if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		for i, a := range data.Artists {
			if query == "label=Legendary" && a.Label != "Legendary" {
				t.Fatal("label filter")
			}
			if query == "country=uk" && !contains(a.Countries, "uk") {
				t.Fatal("country filter")
			}
			if query == "label=Rising+Star" && !a.RisingStar {
				t.Fatal("rising filter")
			}
			if i > 0 && query == "sort=score" && data.Artists[i-1].Score < a.Score {
				t.Fatal("score sort")
			}
			if i > 0 && query == "sort=newest" && data.Artists[i-1].CreationDate < a.CreationDate {
				t.Fatal("year sort")
			}
		}
	}
}

func TestUpstreamFailureAndStaleBanner(t *testing.T) {
	h, err := New(sourceFunc(func(context.Context) (catalog.Snapshot, error) {
		return catalog.Snapshot{}, errors.New("SECRET internal error")
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/artists/1", "/api/artists", "/api/artists/1", "/readyz"} {
		r := request(h, "GET", path)
		if r.Code != 503 || r.Header().Get("Retry-After") == "" || strings.Contains(r.Body.String(), "SECRET") {
			t.Errorf("unsafe outage response %s: %d", path, r.Code)
		}
	}
	for _, path := range []string{"/healthz", "/about", "/static/style.css"} {
		if r := request(h, "GET", path); r.Code != 200 {
			t.Fatal("independent route unavailable")
		}
	}
	base := auditSource(t)
	h, _ = New(sourceFunc(func(ctx context.Context) (catalog.Snapshot, error) {
		s, e := base.Get(ctx)
		s.Stale = true
		return s, e
	}))
	if !strings.Contains(request(h, "GET", "/").Body.String(), "Cached archive") {
		t.Fatal("stale data not labeled")
	}
}

func TestEscapingAndEmptyState(t *testing.T) {
	malicious := `<script>alert("x")</script>`
	source := sourceFunc(func(context.Context) (catalog.Snapshot, error) {
		return catalog.Snapshot{Artists: []catalog.Artist{{ID: 1, Name: malicious, Members: []string{malicious}, CreationDate: 2000}}}, nil
	})
	h, _ := New(source)
	for _, path := range []string{"/", "/artists/1", "/?q=" + url.QueryEscape(malicious)} {
		body := request(h, "GET", path).Body.String()
		if strings.Contains(body, malicious) || !strings.Contains(body, "&lt;script&gt;") {
			t.Fatal("unescaped API text")
		}
	}
	if !strings.Contains(request(h, "GET", "/?q=missing").Body.String(), "No matching artists.") {
		t.Fatal("empty state missing")
	}
}

func TestRecoveryReplacesPartialResponse(t *testing.T) {
	h := protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "secret/type")
		_, _ = w.Write([]byte("SECRET partial page"))
		panic("SECRET panic detail")
	}), 2)
	for _, path := range []string{"/", "/api/artists"} {
		r := request(h, "GET", path)
		if r.Code != 500 || strings.Contains(r.Body.String(), "SECRET") || r.Header().Get("Content-Type") == "secret/type" {
			t.Fatal("panic leaked or incorrect status", r.Code)
		}
	}
	if r := request(h, "HEAD", "/"); r.Code != 500 || r.Body.Len() != 0 {
		t.Fatal("HEAD recovery")
	}
}

func TestConcurrencyLimitAndResponseSize(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release }), 1)
	done := make(chan struct{})
	go func() { request(h, "GET", "/"); close(done) }()
	<-started
	r := request(h, "GET", "/")
	if r.Code != 503 || r.Header().Get("Retry-After") == "" {
		t.Fatal("capacity unbounded")
	}
	close(release)
	<-done
	h = protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, maxPageBytes+1)) }), 1)
	if r := request(h, "GET", "/"); r.Code != 500 {
		t.Fatal("unbounded response")
	}
}

func TestConcurrentRealHTTPStress(t *testing.T) {
	h := newAuditHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var wg sync.WaitGroup
	failures := make(chan string, 2000)
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				path := fmt.Sprintf("/api/artists/%d", (worker+i)%52+1)
				if i%4 == 0 {
					path = "/api/artists?q=queen"
				}
				response, err := client.Get(server.URL + path)
				if err != nil {
					failures <- err.Error()
					continue
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 || !json.Valid(body) {
					failures <- fmt.Sprintf("status=%d err=%v", response.StatusCode, err)
				}
			}
		}(worker)
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if r := request(h, "GET", "/healthz"); r.Code != 200 {
		t.Fatal("server did not survive")
	}
}

func TestRequestTimeReclassification(t *testing.T) {
	snapshot := catalog.Snapshot{Artists: []catalog.Artist{{ID: 1, CreationDate: 2000}}}
	a := makeListing(snapshot, Filters{Sort: "name"}, time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC))
	b := makeListing(snapshot, Filters{Sort: "name"}, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if a.Artists[0].Score+1 != b.Artists[0].Score {
		t.Fatal("scores were cached across a year boundary")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil source accepted")
	}
}

func FuzzQuery(f *testing.F) {
	f.Add("q=queen")
	f.Add("q=%zz")
	f.Add("label=Legendary&sort=score")
	f.Fuzz(func(t *testing.T, raw string) {
		values, err := url.ParseQuery(raw)
		if err == nil {
			_, _ = parseFilters(values)
		}
	})
}
