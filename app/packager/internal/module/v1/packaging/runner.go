package packaging

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"speedapp-packager/app/packager/internal/buildprocess"
	"speedapp-packager/app/packager/internal/config"
	"speedapp-packager/app/packager/internal/content"
)

type RuntimeConfig struct {
	BuildReq
	GetDomin         string                `json:"getdomin"` // 原样保留请求中的第一条域名，供 Cocos 启动引导。
	BuildID          string                `json:"buildId"`
	VersionEndpoints []string              `json:"versionEndpoints"`
	Download         config.DownloadConfig `json:"download"`
	BundleSHA256     string                `json:"bundleSha256"`
}

type Job struct {
	ID           string `json:"id"`
	AppName      string `json:"appName"`
	PackageName  string `json:"packageName"`
	KeystorePath string `json:"keystorePath"`
	AssetsDir    string `json:"assetsDir"`
	BuildDir     string `json:"buildDir"`
	OutputPath   string `json:"outputPath"`
	AllowHTTP    bool   `json:"allowHttp"`
}

type Runner struct {
	cfg        *config.Config
	downloader *content.Downloader
}

var ErrBusy = buildprocess.ErrBusy
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	apkPackageName  = regexp.MustCompile(`(?m)^package: name='([^']+)'`)
	apkAppIcon      = regexp.MustCompile(`(?m)^application-icon-[0-9]+:'([^'\r\n]+)'`)
	apkLauncherIcon = regexp.MustCompile(`(?m)^launchable-activity:[^\r\n]* icon='([^'\r\n]+)'`)
)

func NewRunner(cfg *config.Config) (*Runner, error) {
	d, err := content.NewDownloader(cfg.Download)
	if err != nil {
		return nil, err
	}
	if cfg.VersionURL != "" {
		if _, err := content.WebURL(cfg.VersionURL, cfg.Download.AllowHTTP); err != nil {
			return nil, fmt.Errorf("invalid VersionURL: %w", err)
		}
	}
	if err = os.MkdirAll(filepath.Join(cfg.DataDir, "artifacts"), 0700); err != nil {
		return nil, err
	}
	return &Runner{cfg: cfg, downloader: d}, nil
}

func (r *Runner) Validate(req *BuildReq) error {
	request := *req
	return r.normalizeRequest(&request)
}

func (r *Runner) normalizeRequest(req *BuildReq) error {
	if !versionPattern.MatchString(req.Version) {
		return fmt.Errorf("invalid version")
	}
	if req.DominList == nil || len(req.DominList) > 16 {
		return fmt.Errorf("dominList must be an array with at most 16 backend origins")
	}
	origins := make([]string, len(req.DominList))
	for i, raw := range req.DominList {
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := content.WebURL(raw, r.cfg.Download.AllowHTTP)
		if err != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return fmt.Errorf("dominList must contain backend origins without paths")
		}
		origins[i] = strings.TrimRight(raw, "/")
	}
	if len(req.DominList) == 0 && r.cfg.VersionURL == "" {
		return fmt.Errorf("dominList or configured VersionURL is required for startup version checks")
	}
	if req.URL != "" {
		if _, err := content.WebURL(req.URL, r.cfg.Download.AllowHTTP); err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}
	} else if req.LastVersionURL == "" {
		return fmt.Errorf("last_version_url is required when url is empty")
	}
	if req.LastVersionURL != "" {
		if err := r.downloader.Validate(req.LastVersionURL); err != nil {
			return fmt.Errorf("invalid last_version_url: %w", err)
		}
	}
	if req.URL == "" && len(r.cfg.Download.AllowedOrigins) == 0 {
		return fmt.Errorf("Download.AllowedOrigins is required")
	}
	req.DominList = origins
	return nil
}

func (r *Runner) Ready() error {
	for _, name := range []string{"node", "java", "keytool"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s is unavailable in PATH", name)
		}
	}
	javaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	javaVersion, err := exec.CommandContext(javaCtx, "java", "-version").CombinedOutput()
	if err != nil || !regexp.MustCompile(`version "(17|18|19|20)\.`).Match(javaVersion) {
		return fmt.Errorf("Java 17 is required by this Gradle 8.4/AGP build configuration")
	}
	sdk := androidSDK()
	if sdk == "" {
		return fmt.Errorf("ANDROID_SDK_ROOT is unset")
	}
	if _, err := os.Stat(filepath.Join(sdk, "platforms", "android-33", "android.jar")); err != nil {
		return fmt.Errorf("Android SDK platform android-33 is unavailable")
	}
	tools := filepath.Join(sdk, "build-tools", "33.0.2")
	if _, err := os.Stat(filepath.Join(tools, "lib", "apksigner.jar")); err != nil {
		return fmt.Errorf("Android build-tools 33.0.2 is unavailable")
	}
	if _, err := exec.LookPath(filepath.Join(tools, "aapt2")); err != nil {
		return fmt.Errorf("Android build-tools 33.0.2 aapt2 is unavailable")
	}
	return nil
}

func androidSDK() string {
	if sdk := os.Getenv("ANDROID_SDK_ROOT"); sdk != "" {
		return sdk
	}
	return os.Getenv("ANDROID_HOME")
}

func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Prepare 只准备经过验证的构建输入；不会把下载失败伪装成成功 APK。
func (r *Runner) Prepare(ctx context.Context, req BuildReq, id string) (Job, RuntimeConfig, error) {
	var getDomin string
	if len(req.DominList) > 0 {
		getDomin = req.DominList[0]
	}
	if err := r.normalizeRequest(&req); err != nil {
		return Job{}, RuntimeConfig{}, err
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return Job{}, RuntimeConfig{}, fmt.Errorf("invalid job ID")
	}
	root := filepath.Join(r.cfg.DataDir, "jobs", id)
	job := Job{
		ID:           id,
		AppName:      r.cfg.DefaultAppName,
		PackageName:  fmt.Sprintf("yq.bxgame.com.b%d_%s", time.Now().UnixMilli(), id),
		KeystorePath: filepath.Join(root, "signing.p12"),
		AssetsDir:    filepath.Join(root, "assets"),
		BuildDir:     filepath.Join(root, "build"),
		OutputPath:   filepath.Join(r.cfg.DataDir, "artifacts", id+".apk"),
		AllowHTTP:    r.cfg.Download.AllowHTTP,
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return job, RuntimeConfig{}, err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return job, RuntimeConfig{}, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(root)
		}
	}()
	if err := os.Mkdir(job.AssetsDir, 0700); err != nil {
		return job, RuntimeConfig{}, err
	}
	rc := RuntimeConfig{BuildReq: req, GetDomin: getDomin, BuildID: id, Download: r.cfg.Download}
	if r.cfg.VersionURL != "" {
		rc.VersionEndpoints = []string{r.cfg.VersionURL}
	} else {
		for _, origin := range req.DominList {
			rc.VersionEndpoints = append(rc.VersionEndpoints, origin+r.cfg.VersionPath)
		}
	}
	if req.URL == "" {
		archive := filepath.Join(root, "content.zip")
		hash, err := r.downloader.Download(ctx, req.LastVersionURL, archive)
		if err != nil {
			return job, rc, err
		}
		rc.BundleSHA256 = hash
		files, err := content.Extract(archive, filepath.Join(job.AssetsDir, "html"), r.cfg.Download)
		if err != nil {
			return job, rc, err
		}
		if err = content.WriteJSON(filepath.Join(job.AssetsDir, "pack-files.json"), files); err != nil {
			return job, rc, err
		}
		os.Remove(archive)
	}
	if err := content.WriteJSON(filepath.Join(job.AssetsDir, "pack-config.json"), rc); err != nil {
		return job, rc, err
	}
	if err := content.WriteJSON(filepath.Join(root, "job.json"), job); err != nil {
		return job, rc, err
	}
	keep = true
	return job, rc, nil
}

func (r *Runner) Run(ctx context.Context, req BuildReq) (BuildResp, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(r.cfg.BuildTimeoutSec)*time.Second)
	defer cancel()
	lease, err := buildprocess.Acquire(runCtx, r.cfg.DataDir)
	if err != nil {
		return BuildResp{}, err
	}
	defer lease.Close()
	if err = r.Ready(); err != nil {
		return BuildResp{}, err
	}
	id, err := NewID()
	if err != nil {
		return BuildResp{}, err
	}
	signingPassword, err := NewID()
	if err != nil {
		return BuildResp{}, err
	}
	job, rc, err := r.Prepare(runCtx, req, id)
	if err != nil {
		return BuildResp{}, err
	}
	// Node 正常退出时清除密钥库，服务在工作进程取消后再做一次清理。
	defer os.Remove(job.KeystorePath)
	jobPath := filepath.Join(r.cfg.DataDir, "jobs", id, "job.json")
	command := buildprocess.Command{Args: []string{"node", filepath.Join(r.cfg.ProjectRoot, "index.js"), "--job", jobPath}, Dir: r.cfg.ProjectRoot}
	// 构建进程只接收本次签名密码，不继承 HTTP token 或存储凭据。
	for _, item := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(strings.SplitN(item, "=", 2)[0]), "PACK_") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "PACK_KEYSTORE_STORE_PASS="+signingPassword)
	safeLog, err := lease.Run(command)
	for _, secret := range []string{r.cfg.AuthToken, signingPassword, r.cfg.Storage.SecretKey, r.cfg.Storage.AccessKey} {
		if secret != "" {
			safeLog = strings.ReplaceAll(safeLog, secret, "[REDACTED]")
		}
	}
	os.WriteFile(filepath.Join(filepath.Dir(jobPath), "build.log"), []byte(safeLog), 0600)
	// APK 必须可读且实际含本次配置和所有内置静态文件，不能只凭 Gradle exit code。
	if err != nil {
		return BuildResp{}, fmt.Errorf("Android build failed; inspect jobs/%s/build.log in configured DataDir", id)
	}
	if err = r.verifyBuild(runCtx, job); err != nil {
		os.Remove(job.OutputPath)
		return BuildResp{}, err
	}
	mode := "url"
	if req.URL == "" {
		mode = "local"
	}
	result := BuildResp{ID: id, FileName: id + ".apk", FileURL: LegacyArtifactPrefix + id + ".apk", Mode: mode, Version: req.Version, SHA256: rc.BundleSHA256}
	if err = content.WriteJSON(filepath.Join(filepath.Dir(jobPath), "result.json"), result); err != nil {
		return BuildResp{}, err
	}
	return result, nil
}

func (r *Runner) verifyBuild(ctx context.Context, job Job) error {
	aapt := filepath.Join(androidSDK(), "build-tools", "33.0.2", "aapt2")
	badging, err := exec.CommandContext(ctx, aapt, "dump", "badging", job.OutputPath).Output()
	if err != nil {
		return fmt.Errorf("cannot inspect APK package and launcher icon: %w", err)
	}
	if name := apkPackageName.FindSubmatch(badging); name == nil || string(name[1]) != job.PackageName {
		return fmt.Errorf("APK package name differs from build job")
	}
	appIcons := apkAppIcon.FindAllSubmatch(badging, -1)
	launcher := apkLauncherIcon.FindSubmatch(badging)
	if len(appIcons) == 0 || launcher == nil {
		return fmt.Errorf("APK is missing its app or launcher activity icon")
	}
	icon := filepath.Join(r.cfg.ProjectRoot, "app", "src", "main", "res", "mipmap-xxxhdpi", "ic_launcher.webp")
	resources := map[string]string{string(launcher[1]): icon}
	for _, match := range appIcons {
		resources[string(match[1])] = icon
	}
	return verifyAPK(job.OutputPath, job.AssetsDir, resources)
}

func verifyAPK(apk, assets string, resources map[string]string) error {
	z, err := zip.OpenReader(apk)
	if err != nil {
		return fmt.Errorf("output is not an APK ZIP: %w", err)
	}
	defer z.Close()
	entries := map[string]*zip.File{}
	for _, f := range z.File {
		if entries[f.Name] != nil {
			return fmt.Errorf("duplicate APK entry")
		}
		entries[f.Name] = f
	}
	if entries["AndroidManifest.xml"] == nil || entries["classes.dex"] == nil {
		return fmt.Errorf("output lacks Android application entries")
	}
	for name, source := range resources {
		if err := compareAPKEntry(source, entries[name]); err != nil {
			return fmt.Errorf("APK launcher icon differs from the current template: %w", err)
		}
	}
	return filepath.WalkDir(assets, func(path string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(assets, path)
		return compareAPKEntry(path, entries["assets/"+filepath.ToSlash(rel)])
	})
}

// 资源逐字节比对，包含 ZIP CRC 检查。
func compareAPKEntry(path string, entry *zip.File) error {
	if entry == nil {
		return fmt.Errorf("APK is missing a bundled asset")
	}
	a, err := os.Open(path)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := entry.Open()
	if err != nil {
		return err
	}
	defer b.Close()
	abuf, bbuf := make([]byte, 32768), make([]byte, 32768)
	for {
		an, ae := io.ReadFull(a, abuf)
		bn, be := io.ReadFull(b, bbuf)
		if an != bn || !bytes.Equal(abuf[:an], bbuf[:bn]) {
			return fmt.Errorf("APK asset differs from validated input")
		}
		if ae == io.EOF || ae == io.ErrUnexpectedEOF {
			if be != io.EOF && be != io.ErrUnexpectedEOF {
				return fmt.Errorf("APK asset integrity failure")
			}
			return nil
		}
		if ae != nil || be != nil {
			return fmt.Errorf("APK asset read failure")
		}
	}
}
