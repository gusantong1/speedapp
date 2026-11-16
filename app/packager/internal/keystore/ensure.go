package keystore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"speedapp-packager/app/packager/internal/config"
)

// 已有签名必须复用；自动创建仅在显式启用且提供密码时执行。
func Ensure(c *config.Config) error {
	if _, ok := c.KeystoreOK(); ok {
		return nil
	}
	if !c.AutoGenerateKeystore {
		return nil
	}
	if c.KeystoreStorePass == "" || c.KeystoreKeyPass == "" {
		return fmt.Errorf("keystore generation requires signing passwords")
	}
	if err := os.MkdirAll(filepath.Dir(c.KeystoreAbsPath()), 0700); err != nil {
		return err
	}
	cmd := exec.Command("keytool", "-genkeypair", "-keystore", c.KeystoreAbsPath(), "-alias", c.KeystoreAlias,
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "10000", "-storepass:env", "PACK_KEYSTORE_STORE_PASS",
		"-keypass:env", "PACK_KEYSTORE_KEY_PASS", "-dname", c.KeystoreDN)
	cmd.Env = append(os.Environ(), "PACK_KEYSTORE_STORE_PASS="+c.KeystoreStorePass, "PACK_KEYSTORE_KEY_PASS="+c.KeystoreKeyPass)
	// 不回传 keytool 输出，防止外部工具的诊断泄露密钥。
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("keytool keystore generation failed")
	}
	return nil
}
