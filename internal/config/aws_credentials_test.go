package config

import (
	"testing"

	"github.com/taigrr/crush/internal/env"
)

func TestHasAWSCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		envs map[string]string
		want bool
	}{
		{"no credentials", map[string]string{}, false},
		{"bearer token", map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "tok"}, true},
		{"access key pair", map[string]string{
			"AWS_ACCESS_KEY_ID":     "id",
			"AWS_SECRET_ACCESS_KEY": "secret",
		}, true},
		{"access key id only is insufficient", map[string]string{
			"AWS_ACCESS_KEY_ID": "id",
		}, false},
		{"secret only is insufficient", map[string]string{
			"AWS_SECRET_ACCESS_KEY": "secret",
		}, false},
		{"profile", map[string]string{"AWS_PROFILE": "dev"}, true},
		{"default profile", map[string]string{"AWS_DEFAULT_PROFILE": "dev"}, true},
		{"region", map[string]string{"AWS_REGION": "us-east-1"}, true},
		{"default region", map[string]string{"AWS_DEFAULT_REGION": "us-east-1"}, true},
		{"container relative uri", map[string]string{"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/x"}, true},
		{"container full uri", map[string]string{"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://x"}, true},
		{"empty values ignored", map[string]string{
			"AWS_PROFILE": "",
			"AWS_REGION":  "",
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hasAWSCredentials(env.NewFromMap(tc.envs))
			if got != tc.want {
				t.Fatalf("hasAWSCredentials(%v) = %v, want %v", tc.envs, got, tc.want)
			}
		})
	}
}
