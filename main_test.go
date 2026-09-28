package main

import (
	"context"
	"testing"
)

func TestConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		valid   bool
		address string
	}{
		{"defaults", nil, true, "127.0.0.1:8080"}, {"container", map[string]string{"HOST": "0.0.0.0", "PORT": "9000"}, true, "0.0.0.0:9000"},
		{"IPv6", map[string]string{"HOST": "::1", "PORT": "8081"}, true, "[::1]:8081"},
		{"invalid", map[string]string{"PORT": "abc"}, false, ""}, {"zero", map[string]string{"PORT": "0"}, false, ""}, {"too large", map[string]string{"PORT": "65536"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := readConfig(func(key string) string { return tc.env[key] })
			if (err == nil) != tc.valid {
				t.Fatal("unexpected error", err)
			}
			if tc.valid && c.address != tc.address {
				t.Fatal(c.address)
			}
		})
	}
}

func TestRunBadConfiguration(t *testing.T) {
	if err := run(context.Background(), config{address: "127.0.0.1:0", apiURL: "bad URL"}); err == nil {
		t.Fatal("bad API accepted")
	}
	if err := run(context.Background(), config{address: "invalid address", apiURL: "https://example.org"}); err == nil {
		t.Fatal("bad listen address accepted")
	}
}

func TestGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, config{address: "127.0.0.1:0", apiURL: "https://example.org"}); err != nil {
		t.Fatal(err)
	}
}
