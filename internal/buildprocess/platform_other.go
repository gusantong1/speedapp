//go:build !windows && !linux

package buildprocess

import (
	"fmt"
	"os"
	"os/exec"
)

func hideWindow(*exec.Cmd)                  {}
func unsupported() error                    { return fmt.Errorf("build supervision requires Windows or Linux") }
func containWorker(string) error            { return unsupported() }
func lockFile(*os.File) error               { return unsupported() }
func processBirth(int) (string, error)      { return "", unsupported() }
func sameProcess(int, string) (bool, error) { return false, unsupported() }
func reap(owner, bool) error                { return unsupported() }
