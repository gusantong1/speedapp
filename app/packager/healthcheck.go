package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"speedapp-packager/app/packager/internal/config"
)

func healthcheckURL(c *config.Config) string {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(c.Port)), Path: "/health"}).String()
}

func checkHealth(c *config.Config) error {
	// Health checks target this instance directly, regardless of environment proxies.
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(healthcheckURL(c))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
