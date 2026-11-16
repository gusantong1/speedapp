package content

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"speedapp-packager/app/packager/internal/config"
)

type Downloader struct {
	Config  config.DownloadConfig
	origins map[string]bool
	private []netip.Prefix
	client  *http.Client
}

// WebURL 拒绝带凭据的 URL；域名白名单使用完整 origin（含协议和端口）。
func WebURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return nil, fmt.Errorf("invalid URL")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return nil, fmt.Errorf("HTTPS required")
	}
	if u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(u.Hostname(), ".") || strings.ContainsAny(u.Hostname(), "%\\") {
		return nil, fmt.Errorf("invalid URL host or credentials")
	}
	return u, nil
}

func Origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}

func NewDownloader(cfg config.DownloadConfig) (*Downloader, error) {
	d := &Downloader{Config: cfg, origins: make(map[string]bool)}
	for _, raw := range cfg.AllowedOrigins {
		u, err := WebURL(raw, cfg.AllowHTTP)
		if err != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, fmt.Errorf("Download.AllowedOrigins must contain exact HTTP(S) origins")
		}
		d.origins[Origin(u)] = true
	}
	for _, raw := range cfg.AllowedPrivateCIDRs {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p.Bits() == 0 {
			return nil, fmt.Errorf("invalid AllowedPrivateCIDRs (no /0)")
		}
		d.private = append(d.private, p.Masked())
	}
	transport := &http.Transport{
		Proxy:       nil, // 不借用环境代理，保证实际连接目标经过 DNS/IP 检查。
		DialContext: d.dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DisableCompression: true,
	}
	d.client = &http.Client{Transport: transport, Timeout: time.Duration(cfg.TimeoutSec) * time.Second}
	d.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return fmt.Errorf("too many redirects")
		}
		return d.Validate(req.URL.String())
	}
	return d, nil
}

func (d *Downloader) Validate(raw string) error {
	u, err := WebURL(raw, d.Config.AllowHTTP)
	if err != nil {
		return err
	}
	if !d.origins[Origin(u)] {
		return fmt.Errorf("download origin is not allowed")
	}
	return nil
}

var reserved = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3", "::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func (d *Downloader) allowedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range d.private {
		if p.Contains(ip) {
			return true
		}
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func (d *Downloader) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("download DNS lookup failed")
	}
	// 检查全部解析结果，再直接连接已检查的 IP，防止 DNS rebinding。
	for _, ip := range ips {
		if !d.allowedIP(ip) {
			return nil, fmt.Errorf("download destination IP is blocked")
		}
	}
	var last error
	for _, ip := range ips {
		conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, fmt.Errorf("download connection failed: %w", last)
}

func (d *Downloader) Download(ctx context.Context, raw, target string) (string, error) {
	if err := d.Validate(raw); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", fmt.Errorf("invalid download request")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ZIP download failed (URL omitted)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ZIP download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > d.Config.MaxZipBytes {
		return "", fmt.Errorf("ZIP exceeds compressed size limit")
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(target)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, d.Config.MaxZipBytes+1))
	if err != nil || n < 1 || n > d.Config.MaxZipBytes || (resp.ContentLength >= 0 && n != resp.ContentLength) {
		return "", fmt.Errorf("ZIP download is incomplete or too large")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	ok = true
	return hex.EncodeToString(hash.Sum(nil)), nil
}
