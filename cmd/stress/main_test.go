package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStressReportsMismatchesAndValidatesInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/artists/999999":
			w.WriteHeader(404)
		case r.URL.RawQuery == "label=bad":
			w.WriteHeader(400)
		default:
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	report, err := stress(context.Background(), server.URL, 80, 8)
	if err != nil || report.Failures != 0 || report.Requests != 80 || report.P95MS < 0 {
		t.Fatalf("unexpected report %+v %v", report, err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer broken.Close()
	report, err = stress(context.Background(), broken.URL, 10, 2)
	if err != nil || report.Failures != 10 {
		t.Fatal("missed failed requests")
	}
	for _, base := range []string{"", "file:///tmp", ":bad"} {
		if _, err := stress(context.Background(), base, 10, 2); err == nil {
			t.Fatal("invalid URL accepted")
		}
	}
	if _, err := stress(context.Background(), server.URL, 0, 2); err == nil {
		t.Fatal("empty run accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = stress(ctx, server.URL, 8, 2)
	if err != nil || report.Failures != 8 {
		t.Fatal("cancellation not recorded")
	}
}
