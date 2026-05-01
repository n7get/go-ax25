package main

import (
	"testing"

	"github.com/n7get/go-ax25/ax25"
)

func TestClientLimitReached(t *testing.T) {
	tests := []struct {
		name   string
		active int
		max    int
		want   bool
	}{
		{name: "below positive limit", active: 2, max: 3, want: false},
		{name: "at positive limit", active: 3, max: 3, want: true},
		{name: "zero means unlimited", active: 999, max: 0, want: false},
		{name: "negative is treated as unlimited by checker", active: 1, max: -1, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clientLimitReached(tt.active, tt.max)
			if got != tt.want {
				t.Fatalf("clientLimitReached(%d, %d) = %v, want %v", tt.active, tt.max, got, tt.want)
			}
		})
	}
}

func TestValidateClientLimit(t *testing.T) {
	if err := validateClientLimit(ax25.KeyKissServerMaxClients, 0); err != nil {
		t.Fatalf("validateClientLimit(0): %v", err)
	}
	if err := validateClientLimit(ax25.KeyAgwpeServerMaxClients, 16); err != nil {
		t.Fatalf("validateClientLimit(16): %v", err)
	}
	if err := validateClientLimit(ax25.KeyKissServerMaxClients, -1); err == nil {
		t.Fatalf("validateClientLimit(-1): expected error")
	}
}
