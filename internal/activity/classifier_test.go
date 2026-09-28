package activity

import (
	"testing"
	"time"
)

func TestScoreAndClassify(t *testing.T) {
	tests := []struct {
		name                           string
		spread, concerts, years, score int
		label                          string
	}{
		{"empty", 0, 0, 0, 0, "Emerging"}, {"emerging upper", 5, 0, 0, 10, "Emerging"},
		{"active lower", 4, 1, 0, 11, "Active"}, {"active upper", 2, 7, 0, 25, "Active"},
		{"established lower", 1, 8, 0, 26, "Established"}, {"subject example", 5, 8, 30, 40, "Established"},
		{"legendary lower", 1, 13, 0, 41, "Legendary"}, {"whole years", 0, 0, 29, 5, "Emerging"},
		{"career cap", 0, 0, 500, 10, "Emerging"}, {"negative", -2, -3, -5, 0, "Emerging"},
		{"overflow spread", maxInt, 0, 0, maxInt, "Legendary"}, {"overflow concerts", 0, maxInt, maxInt, maxInt, "Legendary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Score(tc.spread, tc.concerts, tc.years); got != tc.score {
				t.Errorf("score=%d, want %d", got, tc.score)
			}
			if got := Classify(tc.spread, tc.concerts, tc.years); got != tc.label {
				t.Errorf("label=%q, want %q", got, tc.label)
			}
		})
	}
}

func TestRisingStar(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		recent, total, years int
		want                 bool
	}{
		{"7 per year versus 4", 14, 40, 10, true}, {"5 per year versus 4", 10, 40, 10, false},
		{"exactly 50 percent", 12, 40, 10, false}, {"zero career", 10, 10, 0, false},
		{"zero concerts", 0, 0, 10, false}, {"invalid recent", 100, 10, 10, false}, {"negative", -1, 10, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRisingStar(tc.recent, tc.total, tc.years); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRecentWindow(t *testing.T) {
	now := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	dates := []string{"*28-09-2024", "27-09-2024", "28-09-2026", "29-09-2026", "01-01-2025", "garbage", "31-02-2026", "01-01-2025"}
	if got := RecentCount(dates, now); got != 4 {
		t.Fatalf("got %d recent dates, want 4", got)
	}
	leap := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	if got := RecentCount([]string{"28-02-2022", "01-03-2022", "29-02-2024"}, leap); got != 2 {
		t.Fatalf("calendar leap window: %d", got)
	}
}

func FuzzScore(f *testing.F) {
	f.Add(5, 8, 30)
	f.Add(-1, 0, 0)
	f.Add(maxInt, maxInt, maxInt)
	f.Fuzz(func(t *testing.T, a, b, c int) {
		score := Score(a, b, c)
		if score < 0 {
			t.Fatal("score overflowed")
		}
		if Score(a, b, c) != score {
			t.Fatal("score is not deterministic")
		}
		label := Classify(a, b, c)
		if label != "Emerging" && label != "Active" && label != "Established" && label != "Legendary" {
			t.Fatal(label)
		}
		if a >= 0 && a < maxInt && Score(a+1, b, c) < score {
			t.Fatal("adding a country lowered score")
		}
	})
}

func FuzzDate(f *testing.F) {
	f.Add("*28-09-2026")
	f.Add("")
	f.Add("not a date")
	f.Fuzz(func(t *testing.T, raw string) {
		date, err := ParseDate(raw)
		if err == nil {
			if _, err := ParseDate(date.Format("02-01-2006")); err != nil {
				t.Fatal(err)
			}
		}
	})
}
