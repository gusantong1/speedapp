//go:build linux

package buildprocess

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hideWindow(*exec.Cmd)       {}
func containWorker(string) error { return nil }
func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrBusy
	}
	return err
}
func processBirth(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", ErrBusy
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", ErrBusy
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
func sameProcess(pid int, birth string) (bool, error) {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		end := strings.LastIndexByte(string(data), ')')
		if end >= 0 {
			fields := strings.Fields(string(data[end+1:]))
			if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
				return false, nil
			}
		}
	}
	actual, err := processBirth(pid)
	if os.IsNotExist(err) {
		return false, nil
	}
	return actual == birth, err
}

// pidfds target the verified process, even if its numeric PID is subsequently reused.
// Linux 5.3+ is required for automatic orphan recovery; unsupported kernels fail closed.
func reap(record owner, self bool) error {
	if !tokenPattern.MatchString(record.Token) {
		return ErrBusy
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return err
		}
		remaining := 0
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil || (self && pid == os.Getpid()) {
				continue
			}
			base := filepath.Join("/proc", entry.Name())
			stat, err := os.Stat(base)
			if err != nil {
				continue
			}
			if stat.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
				continue
			}
			env, err := os.ReadFile(filepath.Join(base, "environ"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			match := false
			for _, v := range bytes.Split(env, []byte{0}) {
				if string(v) == ownerVariable+"="+record.Token {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			fd, err := unix.PidfdOpen(pid, 0)
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil {
				return err
			}
			// Re-read through /proc after opening the pidfd; refuse a mismatched/reused PID.
			check, err := os.ReadFile(filepath.Join(base, "environ"))
			if err == nil && bytes.Equal(check, env) {
				remaining++
				if err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
					unix.Close(fd)
					return err
				}
			}
			unix.Close(fd)
		}
		if remaining == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrBusy
		}
		time.Sleep(20 * time.Millisecond)
	}
}
