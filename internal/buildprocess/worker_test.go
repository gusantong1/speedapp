package buildprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) { HandleInvocation(); os.Exit(m.Run()) }

// Real subprocess payloads exercise ownership without pretending to build APKs.
func TestWorkerPayload(t *testing.T) {
	mode := os.Getenv("PACK_TEST_PAYLOAD")
	if mode == "" {
		return
	}
	marker := os.Getenv("PACK_TEST_MARKER")
	if mode == "service-crash" {
		lease, err := Acquire(context.Background(), os.Getenv("PACK_TEST_ROOT"))
		if err != nil {
			t.Fatal(err)
		}
		go lease.Run(payloadCommand("tree", marker))
		waitFile(t, marker+"-grandchild")
		os.Exit(91) // no defers: the OS closes the worker control pipe
	}
	if mode == "tree" {
		command := payloadCommand("hold", marker+"-grandchild")
		child := exec.Command(command.Args[0], command.Args[1:]...)
		child.Env = command.Env
		hideWindow(child)
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
	}
	birth, err := processBirth(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(owner{PID: os.Getpid(), Birth: birth})
	if err = os.WriteFile(marker, b, 0600); err != nil {
		t.Fatal(err)
	}
	if mode == "exit" {
		fmt.Println("payload completed")
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func payloadCommand(mode, marker string) Command {
	exe, _ := os.Executable()
	env := append(os.Environ(), "PACK_TEST_PAYLOAD="+mode, "PACK_TEST_MARKER="+marker)
	return Command{Args: []string{exe, "-test.run=^TestWorkerPayload$"}, Env: env}
}
func waitFile(t *testing.T, path string) owner {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			var o owner
			if json.Unmarshal(b, &o) == nil {
				return o
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subprocess did not become ready")
	return owner{}
}
func waitDead(t *testing.T, o owner) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := sameProcess(o.PID, o.Birth)
		if err == nil && !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned subprocess survived cleanup")
}
func mustAcquire(t *testing.T, ctx context.Context, root string) *Lease {
	t.Helper()
	l, err := Acquire(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

func TestKernelLockPreservesActiveOwnerAndRecoversAfterClose(t *testing.T) {
	root := t.TempDir()
	first := mustAcquire(t, context.Background(), root)
	before, _ := os.ReadFile(filepath.Join(root, "build-owner.json"))
	if second, err := Acquire(context.Background(), root); !errors.Is(err, ErrBusy) {
		if second != nil {
			second.Close()
		}
		t.Fatal("active lock not preserved", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "build-owner.json"))
	if string(before) != string(after) {
		t.Fatal("active owner changed")
	}
	first.Close()
	next := mustAcquire(t, context.Background(), root)
	next.Close()
	st, err := os.Stat(filepath.Join(root, "build.lock"))
	if err != nil || st.Size() != 0 {
		t.Fatal("persistent kernel-lock inode removed or changed")
	}
}

func TestConcurrentWorkersHaveExactlyOneOwner(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	start := make(chan struct{})
	winners := make(chan *Lease, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, err := Acquire(context.Background(), root)
			if err == nil {
				winners <- l
			} else if !errors.Is(err, ErrBusy) {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(winners)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	count := 0
	for l := range winners {
		count++
		l.Close()
	}
	if count != 1 {
		t.Fatalf("owners=%d", count)
	}
	if !t.Failed() {
		mustAcquire(t, context.Background(), root).Close()
	}
}

func TestCancellationAndTimeoutReleaseLockAndDescendants(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			var ctx context.Context
			var cancel context.CancelFunc
			if mode == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			lease := mustAcquire(t, ctx, root)
			marker := filepath.Join(root, "payload")
			done := make(chan error, 1)
			go func() { _, err := lease.Run(payloadCommand("tree", marker)); done <- err }()
			parent := waitFile(t, marker)
			grandchild := waitFile(t, marker+"-grandchild")
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled command succeeded")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("cancel hung")
			}
			lease.Close()
			waitDead(t, parent)
			waitDead(t, grandchild)
			mustAcquire(t, context.Background(), root).Close()
		})
	}
}

func TestWorkerCrashSafelyRecoversWithoutStealingOtherBuild(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	other := mustAcquire(t, context.Background(), otherRoot)
	lease := mustAcquire(t, context.Background(), root)
	marker := filepath.Join(root, "payload")
	done := make(chan struct{})
	go func() { lease.Run(payloadCommand("tree", marker)); close(done) }()
	parent := waitFile(t, marker)
	grandchild := waitFile(t, marker+"-grandchild")
	if err := lease.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	lease.Close()
	<-done
	// On Linux this acquisition also reaps token-owned orphan descendants via pidfds.
	mustAcquire(t, context.Background(), root).Close()
	waitDead(t, parent)
	waitDead(t, grandchild)
	if l, err := Acquire(context.Background(), otherRoot); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatal("another valid build was stolen", err)
	}
	other.Close()
}

func TestServiceCrashControlEOFAndRestart(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "payload")
	command := payloadCommand("service-crash", marker)
	cmd := exec.Command(command.Args[0], command.Args[1:]...)
	cmd.Env = append(command.Env, "PACK_TEST_ROOT="+root)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	parent := waitFile(t, marker)
	grandchild := waitFile(t, marker+"-grandchild")
	if cmd.Wait() == nil {
		t.Fatal("service crash fixture exited cleanly")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		l, err := Acquire(context.Background(), root)
		if err == nil {
			l.Close()
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatal("restart could not recover", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	waitDead(t, parent)
	waitDead(t, grandchild)
}

func TestStaleIdentityAndUnfinishedOwnerDoNotKillReusedPID(t *testing.T) {
	root := t.TempDir()
	birth, err := processBirth(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	stale := owner{OS: runtime.GOOS, PID: os.Getpid(), Birth: birth + "-different-incarnation", Token: "0123456789abcdef0123456789abcdef"}
	if err = writeOwner(root, stale); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".build-owner-abandoned.tmp"), []byte("partial"), 0600)
	mustAcquire(t, context.Background(), root).Close()
	alive, err := sameProcess(os.Getpid(), birth)
	if err != nil || !alive {
		t.Fatal("unrelated PID was affected")
	}
}

func TestLegacyAndCorruptStateFailClosedWithoutDeletingAnything(t *testing.T) {
	for _, mode := range []string{"legacy", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := "build.lock"
			if mode == "corrupt" {
				name = "build-owner.json"
			}
			path := filepath.Join(root, name)
			os.WriteFile(path, []byte("unknown active owner"), 0600)
			l, err := Acquire(context.Background(), root)
			if l != nil {
				l.Close()
			}
			if !errors.Is(err, ErrBusy) {
				t.Fatal("ambiguous owner overwritten", err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != "unknown active owner" {
				t.Fatal("ambiguous lock deleted or replaced")
			}
		})
	}
}

func TestNormalExitReleasesLock(t *testing.T) {
	root := t.TempDir()
	l := mustAcquire(t, context.Background(), root)
	logs, err := l.Run(payloadCommand("exit", filepath.Join(root, "payload")))
	if err != nil {
		t.Fatal(err)
	}
	if logs == "" {
		t.Fatal("missing real child output")
	}
	l.Close()
	mustAcquire(t, context.Background(), root).Close()
}
