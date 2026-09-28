// Command stress checks a running tour-track instance under bounded concurrency.
// Run it against your own local/deployed service, never the upstream public API.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type result struct {
	elapsed time.Duration
	failed  bool
}
type summary struct {
	Requests    int     `json:"requests"`
	Concurrency int     `json:"concurrency"`
	Failures    int     `json:"failures"`
	ElapsedMS   int64   `json:"elapsedMs"`
	P50MS       float64 `json:"p50Ms"`
	P95MS       float64 `json:"p95Ms"`
	MaxMS       float64 `json:"maxMs"`
}

func stress(ctx context.Context, base string, total, workers int) (summary, error) {
	report := summary{Requests: total, Concurrency: workers}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || total < 1 || total > 100000 || workers < 1 || workers > 64 {
		return report, fmt.Errorf("use an http(s) URL, 1–100000 requests, and 1–64 workers")
	}
	base = strings.TrimRight(base, "/")
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConns: workers, MaxIdleConnsPerHost: workers}}
	defer client.CloseIdleConnections()
	paths := []struct {
		path   string
		status int
	}{{"/", 200}, {"/api/artists?q=queen", 200}, {"/artists/30", 200}, {"/api/artists/39", 200}, {"/about", 200}, {"/healthz", 200}, {"/artists/999999", 404}, {"/api/artists?label=bad", 400}}
	jobs := make(chan int)
	results := make(chan result, total)
	var wg sync.WaitGroup
	started := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				check := paths[job%len(paths)]
				begin := time.Now()
				failed := false
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+check.path, nil)
				if err != nil {
					results <- result{time.Since(begin), true}
					continue
				}
				response, err := client.Do(req)
				if err != nil {
					failed = true
				} else {
					_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 16<<20))
					response.Body.Close()
					failed = response.StatusCode != check.status || readErr != nil
				}
				results <- result{time.Since(begin), failed}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := 0; i < total; i++ {
			jobs <- i
		}
	}()
	wg.Wait()
	close(results)
	latencies := make([]time.Duration, 0, total)
	for r := range results {
		latencies = append(latencies, r.elapsed)
		if r.failed {
			report.Failures++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report.ElapsedMS = time.Since(started).Milliseconds()
	report.P50MS = float64(latencies[(total-1)*50/100]) / float64(time.Millisecond)
	report.P95MS = float64(latencies[(total-1)*95/100]) / float64(time.Millisecond)
	report.MaxMS = float64(latencies[total-1]) / float64(time.Millisecond)
	return report, nil
}

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "your tour-track base URL")
	total := flag.Int("requests", 1000, "number of requests, at most 100000")
	workers := flag.Int("concurrency", 16, "parallel clients, at most 64")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := stress(ctx, *base, *total, *workers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if report.Failures > 0 {
		os.Exit(1)
	}
}
