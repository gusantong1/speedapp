package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"speedapp-packager/app/packager/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("PACK_TEST_HEALTHCHECK_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHealthcheckURL(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		port int
		want string
	}{
		{"default", "127.0.0.1", 10010, "http://127.0.0.1:10010/health"},
		{"custom-port", "127.0.0.1", 23456, "http://127.0.0.1:23456/health"},
		{"specific-interface", "127.0.0.2", 23456, "http://127.0.0.2:23456/health"},
		{"ipv4-wildcard", "0.0.0.0", 23456, "http://127.0.0.1:23456/health"},
		{"ipv6-loopback", "::1", 23456, "http://[::1]:23456/health"},
		{"ipv6-wildcard", "::", 23456, "http://[::1]:23456/health"},
		{"ipv6-zone", "fe80::1%test-interface", 23456, "http://[fe80::1%25test-interface]:23456/health"},
		{"hostname", "localhost", 23456, "http://localhost:23456/health"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := healthcheckURL(&config.Config{Host: test.host, Port: test.port})
			if got != test.want {
				t.Fatalf("healthcheck URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCheckHealthStatusAndRedirects(t *testing.T) {
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					t.Errorf("unexpected health path: %s", r.URL.Path)
				}
				if status == http.StatusFound {
					w.Header().Set("Location", target.URL)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			// A wildcard listener must be probed through the corresponding loopback address.
			if host == "127.0.0.1" {
				host = "0.0.0.0"
			}
			err = checkHealth(&config.Config{Host: host, Port: port})
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("HTTP %d: healthcheck error = %v", status, err)
			}
		})
	}
	if redirects.Load() != 0 {
		t.Fatal("healthcheck followed a redirect to a different endpoint")
	}
}

func TestHealthcheckCommandLoadsConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected health path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Config{
		Host: host, Port: port, ProjectRoot: root,
		AuthToken:    "healthcheck-test-token-at-least-16",
		KeystorePath: filepath.Join(root, "must-not-be-generated.jks"),
		// Calling signing setup would fail without passwords; liveness must skip it.
		AutoGenerateKeystore: true,
	}
	file := filepath.Join(root, "custom-config.json")
	run := func(override string, want int) {
		t.Helper()
		data, marshalErr := json.Marshal(cfg)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-f", file, "-healthcheck")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(strings.SplitN(value, "=", 2)[0]), "PACK_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "PACK_TEST_HEALTHCHECK_MAIN=1")
		if override != "" {
			cmd.Env = append(cmd.Env, "PACK_HOST="+override)
		}
		output, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			if exit, ok := runErr.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(runErr)
			}
		}
		if code != want {
			t.Fatalf("healthcheck exit = %d, want %d: %s", code, want, output)
		}
	}
	run("", 0)
	cfg.Host = "127.0.0.2"
	run(host, 0) // PACK_HOST must take precedence over the config file.
	server.Close()
	run(host, 1)
	if _, err := os.Stat(cfg.KeystorePath); !os.IsNotExist(err) {
		t.Fatalf("healthcheck changed signing state: %v", err)
	}
}
