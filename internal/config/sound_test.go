package config

import (
	"testing"

	"github.com/taigrr/crush/internal/sound"
)

func TestSoundEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		opts  *Options
		sound sound.Sound
		want  bool
	}{
		{"nil options defaults on", nil, sound.EndOfTurn, true},
		{"nil sound defaults on", &Options{}, sound.EndOfTurn, true},
		{"explicit enabled", &Options{Sound: &SoundOptions{Disabled: false}}, sound.EndOfTurn, true},
		{"master disabled silences all", &Options{Sound: &SoundOptions{Disabled: true}}, sound.Swarm, false},
		{"per-event disabled", &Options{Sound: &SoundOptions{ToolError: &SoundEvent{Disabled: true}}}, sound.ToolError, false},
		{"per-event disabled does not affect others", &Options{Sound: &SoundOptions{ToolError: &SoundEvent{Disabled: true}}}, sound.Blocked, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.opts.SoundEnabled(tt.sound); got != tt.want {
				t.Fatalf("SoundEnabled(%q) = %v, want %v", tt.sound, got, tt.want)
			}
		})
	}
}

func TestSoundPath(t *testing.T) {
	t.Parallel()
	if got := (*Options)(nil).SoundPath(sound.EndOfTurn); got != "" {
		t.Fatalf("nil SoundPath() = %q, want empty", got)
	}
	if got := (&Options{}).SoundPath(sound.EndOfTurn); got != "" {
		t.Fatalf("empty SoundPath() = %q, want empty", got)
	}
	want := "/tmp/done.wav"
	opts := &Options{Sound: &SoundOptions{Swarm: &SoundEvent{Path: want}}}
	if got := opts.SoundPath(sound.Swarm); got != want {
		t.Fatalf("SoundPath(swarm) = %q, want %q", got, want)
	}
	// A path set for one event does not leak to another.
	if got := opts.SoundPath(sound.Blocked); got != "" {
		t.Fatalf("SoundPath(blocked) = %q, want empty", got)
	}
}
