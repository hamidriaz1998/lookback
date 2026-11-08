package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// createNoWindow keeps the console program llama-server from opening a console window next to the app.
	createNoWindow = 0x08000000
	// createSuspended starts the child with its threads suspended, so that it joins the job before it runs.
	createSuspended = 0x00000004
)

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
	jobErr    error

	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
	ntdll                    = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProcess      = ntdll.NewProc("NtResumeProcess")
)

// killOnCloseJob returns the job object that holds every llama-server child. It has
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so Windows kills the children when the app's last handle to the job
// closes, which happens when the app exits for any reason, including a crash or a kill (SPEC section 8). The
// handle is never closed by the app: its closing is the app's death.
func killOnCloseJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("runtime: create job object: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			jobErr = fmt.Errorf("runtime: set job limits: %w", err)
			return
		}
		jobHandle = h
	})
	return jobHandle, jobErr
}

// process is one llama-server child, inside the app's kill-on-close job object.
type process struct {
	pid     int
	logPath string
	exited  chan struct{} // closed after the child has exited and waitErr is set
	waitErr error

	mu     sync.Mutex
	handle windows.Handle // for GetProcessMemoryInfo; closed once the child has exited
}

// startProcess launches llama-server without a console window. The child starts suspended, joins the job object,
// and only then runs, so it cannot outlive the app even if the app dies right after the start.
func startProcess(o Options, port int, key string) (*process, error) {
	if o.LogPath == "" {
		return nil, fmt.Errorf("runtime: no log path")
	}
	job, err := killOnCloseJob()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(o.LogPath), 0o755); err != nil {
		return nil, err
	}
	sink, err := logSinkFor(o.LogPath)
	if err != nil {
		return nil, err
	}
	bin := o.Files.Server
	cmd := exec.Command(bin, argv(o, port, key)...)
	cmd.Dir = filepath.Dir(bin) // the runtime's DLLs sit next to the executable
	cmd.Stdout = sink
	cmd.Stderr = sink
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createSuspended}
	if err := cmd.Start(); err != nil {
		sink.release()
		return nil, fmt.Errorf("runtime: start %s: %w", bin, err)
	}
	p := &process{pid: cmd.Process.Pid, logPath: o.LogPath, exited: make(chan struct{})}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SUSPEND_RESUME, false, uint32(p.pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		sink.release()
		return nil, fmt.Errorf("runtime: open %s: %w", bin, err)
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(h)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		sink.release()
		return nil, fmt.Errorf("runtime: put %s in the job object: %w", bin, err)
	}
	// Go's os/exec has no handle to the child's main thread, so NtResumeProcess resumes it by process handle.
	if r, _, _ := procNtResumeProcess.Call(uintptr(h)); int32(r) < 0 {
		windows.CloseHandle(h)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		sink.release()
		return nil, fmt.Errorf("runtime: resume %s: NTSTATUS %#x", bin, uint32(r))
	}
	p.handle = h
	go func() {
		p.waitErr = cmd.Wait()
		sink.release()
		p.mu.Lock()
		windows.CloseHandle(p.handle)
		p.handle = 0
		p.mu.Unlock()
		close(p.exited)
	}()
	return p, nil
}

// bindCollision reports whether the child failed because its port was taken. llama-server logs "couldn't bind
// HTTP server socket" on every platform.
func (p *process) bindCollision() bool {
	select {
	case <-p.exited:
	default:
		return false
	}
	log := strings.ToLower(trimLogAll(p.logPath))
	return strings.Contains(log, "address already in use") || strings.Contains(log, "bind") ||
		strings.Contains(log, "only one usage of each socket address")
}

// terminate kills the process and waits for the exit or for ctx. A console-less child has no graceful stop
// signal on Windows, so grace does not apply: the server holds no state worth flushing.
func (p *process) terminate(ctx context.Context, grace time.Duration) {
	if p.pid == 0 {
		return
	}
	select {
	case <-p.exited:
		return
	default:
	}
	p.mu.Lock()
	if p.handle != 0 {
		_ = windows.TerminateProcess(p.handle, 1)
	}
	p.mu.Unlock()
	select {
	case <-p.exited:
	case <-ctx.Done():
		<-p.exited // the process was killed above; Wait returns promptly
	}
}

// peakRSS returns the peak working set of the child, in bytes (GetProcessMemoryInfo), or 0 once it has exited.
func (p *process) peakRSS() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == 0 {
		return 0
	}
	var c struct {
		Cb                         uint32
		PageFaultCount             uint32
		PeakWorkingSetSize         uintptr
		WorkingSetSize             uintptr
		QuotaPeakPagedPoolUsage    uintptr
		QuotaPagedPoolUsage        uintptr
		QuotaPeakNonPagedPoolUsage uintptr
		QuotaNonPagedPoolUsage     uintptr
		PagefileUsage              uintptr
		PeakPagefileUsage          uintptr
	}
	c.Cb = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(p.handle), uintptr(unsafe.Pointer(&c)), uintptr(c.Cb))
	if r == 0 {
		return 0
	}
	return int64(c.PeakWorkingSetSize)
}

func trimLogAll(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
