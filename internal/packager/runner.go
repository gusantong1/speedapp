package packager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner struct {
	projectRoot string
	timeout     time.Duration
}

func NewRunner(projectRoot string, timeoutSec int) *Runner {
	return &Runner{
		projectRoot: projectRoot,
		timeout:     time.Duration(timeoutSec) * time.Second,
	}
}

// Run 执行 node index.js 完成 Gradle 构建、反编译、重打包与签名（对齐 Nest spawn 逻辑）
func (r *Runner) Run(ctx context.Context, appName, webviewURL string) error {
	scriptPath := filepath.Join(r.projectRoot, "index.js")
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("打包脚本不存在: %s", scriptPath)
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "node", scriptPath,
		"--appName", appName,
		"--webviewUrl", webviewURL,
		"--agentCode", appName,
	)
	cmd.Dir = r.projectRoot
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	log.Printf("[packager] start node %s appName=%s webviewUrl=%s cwd=%s", scriptPath, appName, webviewURL, r.projectRoot)

	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = stdout.String()
		}
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("打包超时: %w", err)
		}
		return fmt.Errorf("打包进程异常: %w; %s", err, trimLog(msg))
	}
	return nil
}

// ApkPath 与 index.js 输出路径一致：data/agent_apks/{appName}.apk
func (r *Runner) ApkPath(appName string) string {
	return filepath.Join(r.projectRoot, "data", "agent_apks", appName+".apk")
}

func trimLog(s string) string {
	const max = 2048
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
