//go:build linux

package buildprocess

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A killed, unreaped child stays visible in /proc and may deny environ reads.
func TestLinuxReapTerminatesOwnedChild(t *testing.T) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(b)
	birth, err := processBirth(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "owned-child")
	command := payloadCommand("hold", marker)
	child := exec.Command(command.Args[0], command.Args[1:]...)
	child.Env = append(command.Env, ownerVariable+"="+token)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// This direct, unreaped child cannot have its PID reused before Wait.
		_ = unix.Kill(child.Process.Pid, unix.SIGKILL)
		_ = child.Wait()
	})
	childIdentity := waitFile(t, marker)
	if err := reap(owner{OS: "linux", PID: os.Getpid(), Birth: birth, Token: token}, true); err != nil {
		t.Fatalf("owned-child reap error %T: %v", err, err)
	}
	if alive, err := sameProcess(childIdentity.PID, childIdentity.Birth); err != nil || alive {
		t.Fatalf("owned child survived reap: alive=%t error=%v", alive, err)
	}
}

func TestLinuxLiveProcessReadErrorsFailClosed(t *testing.T) {
	for _, readErr := range []error{unix.EACCES, unix.EPERM} {
		if processExited(os.Getpid(), readErr) {
			t.Fatalf("live process permission error was treated as exit: %v", readErr)
		}
	}
}
