//go:build linux

package voice

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	startGrace    = 300 * time.Millisecond
	startPoll     = 15 * time.Millisecond
	pwHelpTimeout = 2 * time.Second
)

type recorder int

const (
	recPwRecord recorder = iota
	recParec
	recArecord
)

func (r recorder) program() string {
	switch r {
	case recPwRecord:
		return "pw-record"
	case recParec:
		return "parec"
	case recArecord:
		return "arecord"
	default:
		return ""
	}
}

func (r recorder) args(rate uint32) []string {
	s := fmt.Sprintf("%d", rate)
	switch r {
	case recPwRecord:
		return []string{"--raw", "--rate", s, "--channels", "1", "--format", "s16", "-"}
	case recParec:
		return []string{"--raw", "--format=s16le", "--rate=" + s, "--channels=1"}
	case recArecord:
		return []string{"-q", "-t", "raw", "-f", "S16_LE", "-c", "1", "-r", s, "-"}
	default:
		return nil
	}
}

type linuxHandle struct {
	stop *atomic.Bool
	cmd  *exec.Cmd
}

func (h *linuxHandle) Stop() {
	if h == nil {
		return
	}
	h.stop.Store(true)
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		go func() { _ = h.cmd.Wait() }()
	}
}

func spawnPCMCapture(sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	recorders := candidateRecorders(binaryOnPath, pwRecordSupportsRaw)
	if len(recorders) == 0 {
		return nil, captureErr("no microphone recorder found on PATH: install pipewire (pw-record), pulseaudio-utils (parec), or alsa-utils (arecord)")
	}
	var failures []string
	for _, rec := range recorders {
		handle, err := trySpawn(rec, sampleRate, pcmCh)
		if err == nil {
			return handle, nil
		}
		failures = append(failures, err.Error())
	}
	return nil, captureErr("could not start a microphone recorder: " + strings.Join(failures, "; "))
}

func trySpawn(rec recorder, sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	cmd := exec.Command(rec.program(), rec.args(sampleRate)...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
	}
	deadline := time.Now().Add(startGrace)
	for time.Now().Before(deadline) {
		var status syscall.WaitStatus
		wpid, werr := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil)
		if werr == nil && wpid == cmd.Process.Pid {
			msg, _ := io.ReadAll(stderr)
			s := strings.TrimSpace(string(msg))
			if s != "" {
				s = ": " + s
			}
			return nil, fmt.Errorf("%s exited immediately (%v)%s", rec.program(), status, s)
		}
		time.Sleep(startPoll)
	}
	stop := &atomic.Bool{}
	go io.Copy(io.Discard, stderr)
	go forwardPCM(stdout, pcmCh, stop)
	return &linuxHandle{stop: stop, cmd: cmd}, nil
}

func candidateRecorders(available func(string) bool, pwRaw func() bool) []recorder {
	pwAvailable := available("pw-record")
	pwLeads := pwAvailable && pwRaw()
	var out []recorder
	if pwLeads {
		out = append(out, recPwRecord)
	}
	if available("parec") {
		out = append(out, recParec)
	}
	if available("arecord") {
		out = append(out, recArecord)
	}
	if pwAvailable && !pwLeads {
		out = append(out, recPwRecord)
	}
	return out
}

func binaryOnPath(name string) bool {
	path := os.Getenv("PATH")
	if path == "" {
		return false
	}
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

func pwRecordSupportsRaw() bool {
	cmd := exec.Command("pw-record", "--help")
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := combinedOutputTimeout(cmd, pwHelpTimeout)
	if err != nil && len(out) == 0 {
		return false
	}
	return strings.Contains(string(out), "--raw")
}

func combinedOutputTimeout(cmd *exec.Cmd, d time.Duration) ([]byte, error) {
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(d):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return out, fmt.Errorf("timeout")
	}
}

func forwardPCM(r io.Reader, pcmCh chan<- []byte, stop *atomic.Bool) {
	buf := make([]byte, captureReadChunk)
	var dropped atomic.Uint64
	for {
		if stop.Load() {
			return
		}
		n, err := r.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			trySendPCM(pcmCh, chunk, &dropped)
		}
		if err != nil {
			return
		}
	}
}
