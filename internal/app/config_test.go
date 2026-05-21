//go:build windows

package app

import "testing"

func TestNormalizeAccentColor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default empty", in: "", want: defaultAccentColor},
		{name: "six digits", in: "00aaff", want: "#00aaff"},
		{name: "hash six digits", in: "#ABCDEF", want: "#abcdef"},
		{name: "short hash", in: "#fa3", want: "#ffaa33"},
		{name: "invalid", in: "not-a-color", want: defaultAccentColor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAccentColor(tt.in); got != tt.want {
				t.Fatalf("normalizeAccentColor(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveSubscriptionInput(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantURL     string
		wantProfile string
		wantVersion string
		wantErr     bool
	}{
		{name: "empty", in: "", wantURL: ""},
		{name: "http", in: "https://example.com/config.json", wantURL: "https://example.com/config.json"},
		{
			name:        "import remote profile",
			in:          "sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fsub.json&version=1.11.0#Work",
			wantURL:     "https://example.com/sub.json",
			wantProfile: "Work",
			wantVersion: "1.11.0",
		},
		{name: "unsupported scheme", in: "ftp://example.com/config.json", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotProfile, gotVersion, err := resolveSubscriptionInput(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSubscriptionInput(%q) expected error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSubscriptionInput(%q) unexpected error: %v", tt.in, err)
			}
			if gotURL != tt.wantURL || gotProfile != tt.wantProfile || gotVersion != tt.wantVersion {
				t.Fatalf("resolveSubscriptionInput(%q) = (%q, %q, %q), want (%q, %q, %q)", tt.in, gotURL, gotProfile, gotVersion, tt.wantURL, tt.wantProfile, tt.wantVersion)
			}
		})
	}
}

func TestBytesPerSecond(t *testing.T) {
	if got := bytesPerSecond(1500, 1.5); got != 1000 {
		t.Fatalf("bytesPerSecond(1500, 1.5) = %d, want 1000", got)
	}
	if got := bytesPerSecond(-1, 1); got != 0 {
		t.Fatalf("bytesPerSecond(-1, 1) = %d, want 0", got)
	}
	if got := bytesPerSecond(10, 0); got != 0 {
		t.Fatalf("bytesPerSecond(10, 0) = %d, want 0", got)
	}
}
