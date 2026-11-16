//go:build windows

package buildprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var kernel = windows.NewLazySystemDLL("kernel32.dll")
var openJob = kernel.NewProc("OpenJobObjectW")
var isInJob = kernel.NewProc("IsProcessInJob")

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func lockFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}
func processBirth(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	return birthOf(h)
}
func birthOf(h windows.Handle) (string, error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x%08x", c.HighDateTime, c.LowDateTime), nil
}
func sameProcess(pid int, birth string) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	actual, err := birthOf(h)
	if err != nil {
		return false, err
	}
	if actual != birth {
		return false, nil
	}
	status, err := windows.WaitForSingleObject(h, 0)
	return status == uint32(windows.WAIT_TIMEOUT), err
}
func jobName(token string) *uint16 {
	p, _ := windows.UTF16PtrFromString("Global\\packapp-build-" + token)
	return p
}

func containWorker(token string) error {
	h, err := windows.CreateJobObject(nil, jobName(token))
	if err != nil {
		return err
	}
	// The worker itself joins before it can create children: no spawn/assignment crash gap.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return err
	}
	process, err := windows.GetCurrentProcess()
	if err != nil {
		windows.CloseHandle(h)
		return err
	}
	if err = windows.AssignProcessToJobObject(h, process); err != nil {
		windows.CloseHandle(h)
		return err
	}
	// Retained until worker process exit. Closing it here would also terminate the worker.
	return nil
}

func reap(record owner, self bool) error {
	if !tokenPattern.MatchString(record.Token) {
		return ErrBusy
	}
	h, _, openErr := openJob.Call(0x0004|0x0008, 0, uintptr(unsafe.Pointer(jobName(record.Token)))) // QUERY | TERMINATE
	if h == 0 {
		if errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		return openErr
	}
	job := windows.Handle(h)
	defer windows.CloseHandle(job)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list struct {
			Assigned uint32
			Count    uint32
			IDs      [2048]uintptr
		}
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil)
		if err != nil {
			return err
		}
		if list.Count > uint32(len(list.IDs)) {
			return ErrBusy
		}
		remaining := 0
		for _, id := range list.IDs[:list.Count] {
			if self && int(id) == os.Getpid() {
				continue
			}
			remaining++
			p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(id))
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			if err != nil {
				return err
			}
			// A handle pins this process incarnation; verify job membership again before killing.
			var member int32
			ok, _, checkErr := isInJob.Call(uintptr(p), h, uintptr(unsafe.Pointer(&member)))
			if ok == 0 {
				windows.CloseHandle(p)
				return checkErr
			}
			if member != 0 {
				windows.TerminateProcess(p, 1)
			}
			windows.CloseHandle(p)
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
