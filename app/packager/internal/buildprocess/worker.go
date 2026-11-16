// Package buildprocess keeps the kernel lock and build descendants in a dedicated worker.
package buildprocess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const workerFlag = "-packapp-build-worker"
const ownerVariable = "PACK_BUILD_OWNER"

var ErrBusy = errors.New("another build holds the project lock or its recovery is pending")
var tokenPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Command struct {
	Args []string
	Dir  string
	Env  []string
}
type response struct {
	Code string
	Log  string
}
type owner struct {
	OS       string
	PID      int
	Birth    string
	Token    string
	Finished bool
}

type Lease struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *os.File
	decoder *json.Decoder
	done    chan struct{}
	stop    func() bool
	once    sync.Once
}

// Acquire never unlinks build.lock. Kernel ownership, not file existence, serializes workers.
func Acquire(ctx context.Context, root string) (*Lease, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)
	cmd := exec.Command(executable, workerFlag)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(strings.SplitN(value, "=", 2)[0]), "PACK_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, ownerVariable+"="+token)
	hideWindow(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, writer, err := os.Pipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	cmd.Stdout = writer
	cmd.Stderr = io.Discard // only the structured protocol may leave the worker
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		writer.Close()
		return nil, err
	}
	writer.Close()
	l := &Lease{cmd: cmd, in: in, out: out, decoder: json.NewDecoder(out), done: make(chan struct{})}
	go func() { cmd.Wait(); close(l.done) }()
	l.stop = context.AfterFunc(ctx, func() { in.Close() })
	startup := time.AfterFunc(15*time.Second, func() { in.Close(); cmd.Process.Kill() })
	defer startup.Stop()
	if err = json.NewEncoder(in).Encode(struct{ Root string }{root}); err == nil {
		var ready response
		err = l.decoder.Decode(&ready)
		if err == nil && ready.Code == "busy" {
			err = ErrBusy
		}
		if err == nil && ready.Code != "ready" {
			err = fmt.Errorf("build worker could not establish safe process ownership (%s)", ready.Code)
		}
	}
	if err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (l *Lease) Run(command Command) (string, error) {
	if err := json.NewEncoder(l.in).Encode(command); err != nil {
		return "", err
	}
	var result response
	if err := l.decoder.Decode(&result); err != nil {
		return "", fmt.Errorf("build worker stopped: %w", err)
	}
	if result.Code != "ok" {
		return result.Log, fmt.Errorf("build command failed or was cancelled")
	}
	return result.Log, nil
}

func (l *Lease) Close() {
	l.once.Do(func() {
		l.stop()
		l.in.Close() // EOF also cancels the worker after a service crash/disconnect.
		select {
		case <-l.done:
		case <-time.After(20 * time.Second):
			l.cmd.Process.Kill()
			<-l.done
		}
		l.out.Close()
	})
}

// HandleInvocation must run before flag parsing, also in subprocess tests.
func HandleInvocation() {
	if len(os.Args) == 2 && os.Args[1] == workerFlag {
		runWorker()
		os.Exit(0)
	}
}

func runWorker() {
	encoder := json.NewEncoder(os.Stdout)
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 4<<20))
	var hello struct{ Root string }
	if decoder.Decode(&hello) != nil || !filepath.IsAbs(hello.Root) {
		encoder.Encode(response{Code: "invalid"})
		return
	}
	token := os.Getenv(ownerVariable)
	if !tokenPattern.MatchString(token) {
		encoder.Encode(response{Code: "invalid"})
		return
	}
	if err := containWorker(token); err != nil {
		encoder.Encode(response{Code: "containment"})
		return
	}
	lock, record, err := acquireLock(hello.Root, token)
	if err != nil {
		code := "lock-error"
		if errors.Is(err, ErrBusy) {
			code = "busy"
		}
		encoder.Encode(response{Code: code})
		return
	}
	defer lock.Close()
	if encoder.Encode(response{Code: "ready"}) != nil {
		finish(lock, hello.Root, record)
		return
	}
	var command Command
	if decoder.Decode(&command) != nil {
		finish(lock, hello.Root, record)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { var ignored any; decoder.Decode(&ignored); cancel() }()
	var logs tailBuffer
	result := "failed"
	if len(command.Args) > 0 {
		cmd := exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
		cmd.Dir = command.Dir
		cmd.Env = append(command.Env, ownerVariable+"="+token)
		cmd.Stdout, cmd.Stderr = &logs, &logs
		hideWindow(cmd)
		cmd.Cancel = func() error { return reap(record, true) }
		cmd.WaitDelay = 5 * time.Second
		if cmd.Run() == nil && ctx.Err() == nil {
			result = "ok"
		}
	}
	if !finish(lock, hello.Root, record) {
		result = "cleanup-pending"
	}
	encoder.Encode(response{Code: result, Log: string(logs.data)})
}

func acquireLock(root, token string) (*os.File, owner, error) {
	f, err := os.OpenFile(filepath.Join(root, "build.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, owner{}, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	if err = lockFile(f); err != nil {
		return nil, owner{}, err
	}
	// Legacy O_EXCL files cannot prove descendant ownership. Never erase an ambiguous lock.
	st, err := f.Stat()
	if err != nil {
		return nil, owner{}, err
	}
	if st.Size() != 0 {
		return nil, owner{}, ErrBusy
	}
	previous := owner{}
	statePath := filepath.Join(root, "build-owner.json")
	if info, checkErr := os.Stat(statePath); checkErr == nil && info.Size() > 4096 {
		return nil, owner{}, ErrBusy
	}
	data, err := os.ReadFile(statePath)
	if err == nil {
		if json.Unmarshal(data, &previous) != nil || !tokenPattern.MatchString(previous.Token) {
			return nil, owner{}, ErrBusy
		}
		if !previous.Finished {
			if previous.OS != runtime.GOOS {
				return nil, owner{}, ErrBusy
			}
			alive, checkErr := sameProcess(previous.PID, previous.Birth)
			if checkErr != nil || alive {
				return nil, owner{}, ErrBusy
			}
			if reap(previous, false) != nil {
				return nil, owner{}, ErrBusy
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, owner{}, err
	}
	birth, err := processBirth(os.Getpid())
	if err != nil {
		return nil, owner{}, err
	}
	record := owner{OS: runtime.GOOS, PID: os.Getpid(), Birth: birth, Token: token}
	if err = writeOwner(root, record); err != nil {
		return nil, owner{}, err
	}
	ok = true
	return f, record, nil
}

func finish(lock *os.File, root string, record owner) bool {
	if reap(record, true) != nil {
		return false
	}
	record.Finished = true
	if writeOwner(root, record) != nil {
		return false
	}
	return true
}

func writeOwner(root string, record owner) error {
	f, err := os.CreateTemp(root, ".build-owner-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err = json.NewEncoder(f).Encode(record); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(root, "build-owner.json"))
}

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > 256<<10 {
		b.data = b.data[len(b.data)-(256<<10):]
	}
	return len(p), nil
}
