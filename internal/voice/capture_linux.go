//go:build linux

package voice

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	startGrace       = 300 * time.Millisecond
	startPoll        = 15 * time.Millisecond
	pwHelpTimeout    = 2 * time.Second
	captureReadChunk = 2048
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

func (r recorder) args(rate uint32, device linuxDevice) []string {
	s := fmt.Sprintf("%d", rate)
	switch r {
	case recPwRecord:
		out := []string{"--raw", "--rate", s, "--channels", "1", "--format", "s16"}
		if device.name != "" {
			out = append(out, "--target", device.name)
		}
		return append(out, "-")
	case recParec:
		out := []string{"--raw", "--format=s16le", "--rate=" + s, "--channels=1"}
		if device.name != "" {
			out = append(out, "--device="+device.name)
		}
		return out
	case recArecord:
		out := []string{"-q", "-t", "raw", "-f", "S16_LE", "-c", "1", "-r", s}
		if device.name != "" {
			out = append(out, "-D", device.name)
		}
		return append(out, "-")
	default:
		return nil
	}
}

func (r recorder) accepts(device linuxDevice) bool {
	if device.name == "" {
		return true
	}
	switch r {
	case recPwRecord, recParec:
		return device.backend == linuxBackendPulse
	case recArecord:
		return device.backend == linuxBackendALSA
	default:
		return false
	}
}

type linuxHandle struct {
	once sync.Once
	cmd  *exec.Cmd
}

// The stdout reader closes the stream once the pipe drains, so audio
// recorded before the kill is still delivered.
func (h *linuxHandle) Stop() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.cmd != nil && h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
			go func() { _ = h.cmd.Wait() }()
		}
	})
}

func spawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	device, err := parseLinuxDeviceID(deviceID)
	if err != nil {
		return nil, nil, err
	}
	recorders := candidateRecorders(binaryOnPath, pwRecordSupportsRaw)
	if len(recorders) == 0 {
		return nil, nil, captureErr("no microphone recorder found on PATH: install pipewire (pw-record), pulseaudio-utils (parec), or alsa-utils (arecord)")
	}
	var failures []string
	tried := 0
	for _, rec := range recorders {
		if !rec.accepts(device) {
			continue
		}
		tried++
		handle, pcm, err := trySpawn(rec, sampleRate, device)
		if err == nil {
			return handle, pcm, nil
		}
		failures = append(failures, err.Error())
	}
	if tried == 0 {
		return nil, nil, deviceUnavailableErr(deviceID)
	}
	if device.name != "" {
		err := deviceUnavailableErr(deviceID)
		err.Msg += ": " + strings.Join(failures, "; ")
		return nil, nil, err
	}
	return nil, nil, captureErr("could not start a microphone recorder: " + strings.Join(failures, "; "))
}

func trySpawn(rec recorder, sampleRate uint32, device linuxDevice) (CaptureHandle, <-chan []byte, error) {
	cmd := exec.Command(rec.program(), rec.args(sampleRate, device)...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start %s: %w", rec.program(), err)
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
			return nil, nil, fmt.Errorf("%s exited immediately (%v)%s", rec.program(), status, s)
		}
		time.Sleep(startPoll)
	}
	stream := newPCMStream(captureBuffer)
	go io.Copy(io.Discard, stderr)
	go forwardPCM(stdout, stream)
	return &linuxHandle{cmd: cmd}, stream.C(), nil
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

func forwardPCM(r io.Reader, stream *pcmStream) {
	defer stream.close()
	buf := make([]byte, captureReadChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			stream.push(chunk)
		}
		if err != nil {
			return
		}
	}
}
