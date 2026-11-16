package packaging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"speedapp-packager/app/packager/internal/config"
	"speedapp-packager/app/packager/internal/storage"
)

// LegacyArtifactPrefix 是旧接口及磁盘作业记录中的本地下载地址前缀。
const LegacyArtifactPrefix = "/artifacts/"

var artifactName = regexp.MustCompile(`^[0-9a-f]{32}\.apk$`)

type Builder interface {
	Validate(*BuildReq) error
	Ready() error
	Run(context.Context, BuildReq) (BuildResp, error)
}

type failureKind int

const (
	invalidInput failureKind = iota
	rateLimited
	buildBusy
	notReady
	artifactUnavailable
	uploadFailed
	buildFailed
)

type buildFailure struct {
	kind failureKind
	err  error
}

// Logic 管理构建、产物和就绪检查。所有路由共享构建名额与限流窗口。
type Logic struct {
	cfg    *config.Config
	runner Builder
	store  *storage.Client
	slot   chan struct{}
	mu     sync.Mutex
	window time.Time
	count  int
}

func NewLogic(cfg *config.Config, runner Builder, store *storage.Client) *Logic {
	return &Logic{cfg: cfg, runner: runner, store: store, slot: make(chan struct{}, 1)}
}

func (l *Logic) Build(ctx context.Context, req BuildReq) (BuildResp, *buildFailure) {
	if err := l.runner.Validate(&req); err != nil {
		return BuildResp{}, &buildFailure{kind: invalidInput, err: err}
	}
	if !l.allowBuild() {
		return BuildResp{}, &buildFailure{kind: rateLimited}
	}
	select {
	case l.slot <- struct{}{}:
		defer func() { <-l.slot }()
	default:
		return BuildResp{}, &buildFailure{kind: buildBusy, err: errors.New("a build is already running")}
	}
	if err := l.runner.Ready(); err != nil {
		return BuildResp{}, &buildFailure{kind: notReady, err: err}
	}
	result, err := l.runner.Run(ctx, req)
	if errors.Is(err, ErrBusy) {
		return BuildResp{}, &buildFailure{kind: buildBusy, err: err}
	}
	if err != nil {
		return BuildResp{}, &buildFailure{kind: buildFailed, err: err}
	}
	if l.store != nil {
		return l.uploadAPK(ctx, result)
	}
	return result, nil
}

func (l *Logic) allowBuild() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.window) >= time.Minute {
		l.window = time.Now()
		l.count = 0
	}
	if l.count >= 10 {
		return false
	}
	l.count++
	return true
}

func (l *Logic) uploadAPK(ctx context.Context, result BuildResp) (BuildResp, *buildFailure) {
	f, err := os.Open(filepath.Join(l.cfg.DataDir, "artifacts", result.FileName))
	if err != nil {
		return BuildResp{}, &buildFailure{kind: artifactUnavailable, err: err}
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return BuildResp{}, &buildFailure{kind: artifactUnavailable, err: err}
	}
	_, remote, err := l.store.UploadApk(ctx, l.cfg.DefaultAppName, result.ID, f, stat.Size(), "")
	if err != nil {
		return result, &buildFailure{kind: uploadFailed, err: err}
	}
	result.FileURL = remote
	return result, nil
}

func (l *Logic) Artifact(name string) (string, bool) {
	if !artifactName.MatchString(name) {
		return "", false
	}
	path := filepath.Join(l.cfg.DataDir, "artifacts", name)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func (l *Logic) Ready(ctx context.Context) error {
	if err := l.runner.Ready(); err != nil {
		return err
	}
	if l.store != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := l.store.Ping(ctx); err != nil {
			return errors.New("MinIO unavailable")
		}
	}
	return nil
}
