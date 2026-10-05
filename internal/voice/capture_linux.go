//go:build linux

package voice

import (
	"encoding/binary"
	"strings"
	"sync"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

type linuxHandle struct {
	once   sync.Once
	client *pulse.Client
	rec    *pulse.RecordStream
	stream *pcmStream
}

func (h *linuxHandle) Stop() {
	h.once.Do(func() {
		h.rec.Stop()
		h.rec.Close()
		h.client.Close()
		h.stream.close()
	})
}

func pulseClient() (*pulse.Client, error) {
	c, err := pulse.NewClient(pulse.ClientApplicationName("crush"))
	if err != nil {
		return nil, captureErr("PulseAudio/PipeWire: " + err.Error())
	}
	return c, nil
}

func spawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	client, err := pulseClient()
	if err != nil {
		return nil, nil, err
	}
	opts := []pulse.RecordOption{
		pulse.RecordSampleRate(int(sampleRate)),
		pulse.RecordChannels(proto.ChannelMap{proto.ChannelMono}),
		pulse.RecordLatency(0.05),
	}
	if deviceID != "" {
		src, err := client.SourceByID(deviceID)
		if err != nil {
			client.Close()
			return nil, nil, deviceUnavailableErr(deviceID)
		}
		opts = append(opts, pulse.RecordSource(src))
	}
	stream := newPCMStream(captureBuffer)
	rec, err := client.NewRecord(pulse.Int16Writer(func(samples []int16) (int, error) {
		out := make([]byte, len(samples)*2)
		for i, v := range samples {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
		}
		stream.push(out)
		return len(samples), nil
	}), opts...)
	if err != nil {
		client.Close()
		return nil, nil, captureErr("record stream: " + err.Error())
	}
	rec.Start()
	return &linuxHandle{client: client, rec: rec, stream: stream}, stream.C(), nil
}

func listInputDevices() ([]InputDevice, error) {
	client, err := pulseClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	sources, err := client.ListSources()
	if err != nil {
		return nil, captureErr("list sources: " + err.Error())
	}
	defaultID := ""
	if def, err := client.DefaultSource(); err == nil {
		defaultID = def.ID()
	}
	out := make([]InputDevice, 0, len(sources))
	for _, s := range sources {
		if strings.HasSuffix(s.ID(), ".monitor") {
			continue
		}
		out = append(out, InputDevice{ID: s.ID(), Name: s.Name(), Default: s.ID() == defaultID})
	}
	return out, nil
}
