package keystore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"speedapp-packager/internal/config"
)

// Ensure 在配置允许且文件不存在时，用 keytool 生成 keystore（别名/密码与 app/build.gradle.kts、index.js 一致）
func Ensure(cfg *config.Config) error {
	abs := cfg.KeystoreAbsPath()

	if st, err := os.Stat(abs); err == nil {
		if st.IsDir() {
			return fmt.Errorf("keystore 路径是目录而非文件: %s；请在宿主机执行 rm -rf 该路径后重启，或取消错误的 -v 挂载", abs)
		}
		return nil
	}

	if !cfg.AutoGenerateKeystore {
		return fmt.Errorf("keystore 不存在: %s", abs)
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("创建 keystore 目录: %w", err)
	}

	alias := cfg.KeystoreAlias
	storePass := cfg.KeystoreStorePass
	keyPass := cfg.KeystoreKeyPass
	dname := cfg.KeystoreDN

	args := []string{
		"-genkeypair", "-v",
		"-keystore", abs,
		"-alias", alias,
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "36500",
		"-storepass", storePass,
		"-keypass", keyPass,
		"-dname", dname,
	}

	cmd := exec.Command("keytool", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("keytool 生成 keystore 失败: %w; %s", err, string(out))
	}
	return nil
}
