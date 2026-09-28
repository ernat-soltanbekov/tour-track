// Package activity implements the subject's transparent, rule-based classifier.
// These labels describe the supplied dataset, not an artist's present-day career.
package activity

import (
	"strings"
	"time"
)

const maxInt = int(^uint(0) >> 1)

// Score counts countries twice, concert dates three times, and adds at most ten
// points for career length. Integer division is intentional: 29 / 5 equals 5.
// Invalid negative inputs become zero; extreme integers saturate, never wrap.
func Score(tourSpread, concertCount, careerYears int) int {
	career := min(10, max(0, careerYears)/5)
	spread := max(0, tourSpread)
	concerts := max(0, concertCount)
	if spread > (maxInt-career)/2 {
		return maxInt
	}
	total := spread*2 + career
	if concerts > (maxInt-total)/3 {
		return maxInt
	}
	return total + concerts*3
}

// Classify returns exactly one of the four labels required by the subject.
func Classify(tourSpread, concertCount, careerYears int) string {
	switch score := Score(tourSpread, concertCount, careerYears); {
	case score <= 10:
		return "Emerging"
	case score <= 25:
		return "Active"
	case score <= 40:
		return "Established"
	default:
		return "Legendary"
	}
}

// ParseDate accepts the API's day-month-year format and its optional * marker.
func ParseDate(value string) (time.Time, error) {
	return time.Parse("02-01-2006", strings.TrimPrefix(strings.TrimSpace(value), "*"))
}

// IsRisingStar compares a two-year rate with the lifetime rate. A strict > is
// required: exactly 50% higher is not a Rising Star. A zero-length career has no
// defined lifetime rate, so it is not flagged.
func IsRisingStar(recentConcerts, concertCount, careerYears int) bool {
	if recentConcerts <= 0 || concertCount <= 0 || careerYears <= 0 || recentConcerts > concertCount {
		return false
	}
	return float64(recentConcerts)/2 > (float64(concertCount)/float64(careerYears))*1.5
}

// RecentCount uses a rolling two-calendar-year window, inclusive at both ends.
// Dates are calendar days in UTC. Future concerts and malformed dates do not
// count as completed recent activity; repeated dates remain separate entries.
func RecentCount(dates []string, now time.Time) int {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(-2, 0, 0)
	count := 0
	for _, raw := range dates {
		date, err := ParseDate(raw)
		if err == nil && !date.Before(start) && !date.After(today) {
			count++
		}
	}
	return count
}
