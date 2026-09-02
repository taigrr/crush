package config

import "testing"

func TestSoundEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts *Options
		want bool
	}{
		{"nil options defaults on", nil, true},
		{"nil sound defaults on", &Options{}, true},
		{"explicit enabled", &Options{Sound: &SoundOptions{Disabled: false}}, true},
		{"explicit disabled", &Options{Sound: &SoundOptions{Disabled: true}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.opts.SoundEnabled(); got != tt.want {
				t.Fatalf("SoundEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSoundPath(t *testing.T) {
	t.Parallel()
	if got := (*Options)(nil).SoundPath(); got != "" {
		t.Fatalf("nil SoundPath() = %q, want empty", got)
	}
	if got := (&Options{}).SoundPath(); got != "" {
		t.Fatalf("empty SoundPath() = %q, want empty", got)
	}
	want := "/tmp/done.wav"
	if got := (&Options{Sound: &SoundOptions{Path: want}}).SoundPath(); got != want {
		t.Fatalf("SoundPath() = %q, want %q", got, want)
	}
}
