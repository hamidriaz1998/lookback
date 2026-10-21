//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// process is one llama-server child. The goroutine that forks it locks its OS thread and keeps the thread for
// the child's whole life: Pdeathsig fires when the forking thread exits, so the child dies with the app even
// after kill -9 (SPEC section 8). That goroutine returns only after Wait, when the child is gone.
type process struct {
	pid     int
	logPath string
	exited  chan struct{} // closed after the child has exited and waitErr is set
	waitErr error
}

// startProcess launches llama-server in its own process group.
func startProcess(o Options, port int, key string) (*process, error) {
	if o.LogPath == "" {
		return nil, fmt.Errorf("runtime: no log path")
	}
	if err := os.MkdirAll(filepath.Dir(o.LogPath), 0o755); err != nil {
		return nil, err
	}
	rotateLog(o.LogPath)
	p := &process{logPath: o.LogPath, exited: make(chan struct{})}
	started := make(chan error, 1)
	bin := o.Files.Server
	args := argv(o, port, key)
	go func() {
		goruntime.LockOSThread()
		logFile, err := os.OpenFile(o.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			started <- err
			close(p.exited)
			return
		}
		defer logFile.Close()
		cmd := exec.Command(bin, args...)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
		if err := cmd.Start(); err != nil {
			started <- fmt.Errorf("runtime: start %s: %w", bin, err)
			close(p.exited)
			return
		}
		p.pid = cmd.Process.Pid
		started <- nil
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}

// bindCollision reports whether the child failed because its port was taken.
func (p *process) bindCollision() bool {
	select {
	case <-p.exited:
	default:
		return false
	}
	log := strings.ToLower(trimLogAll(p.logPath))
	return strings.Contains(log, "address already in use") || strings.Contains(log, "bind")
}

// terminate sends SIGTERM to the process group, then SIGKILL after grace, and waits for the exit or for ctx.
func (p *process) terminate(ctx context.Context, grace time.Duration) {
	if p.pid == 0 {
		return
	}
	select {
	case <-p.exited:
		return
	default:
	}
	_ = syscall.Kill(-p.pid, syscall.SIGTERM)
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-p.exited:
	case <-t.C:
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		<-p.exited
	case <-ctx.Done():
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		<-p.exited
	}
}

// peakRSS returns VmHWM of the child from /proc, in bytes.
func (p *process) peakRSS() int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(p.pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmHWM:" {
			v, _ := strconv.ParseInt(f[1], 10, 64)
			return v << 10
		}
	}
	return 0
}

func trimLogAll(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
