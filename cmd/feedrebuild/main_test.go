package main

import (
	"reflect"
	"testing"
)

func TestUint64ListFlagSet(t *testing.T) {
	var flags uint64ListFlag

	if err := flags.Set("1,2"); err != nil {
		t.Fatalf("set comma-separated IDs: %v", err)
	}
	if err := flags.Set("3"); err != nil {
		t.Fatalf("set repeated flag: %v", err)
	}

	want := uint64ListFlag{1, 2, 3}
	if !reflect.DeepEqual(flags, want) {
		t.Fatalf("expected %v, got %v", want, flags)
	}
}

func TestUint64ListFlagSetRejectsInvalidValue(t *testing.T) {
	tests := []string{"", "abc", "0", "1,-2"}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			var flags uint64ListFlag
			if err := flags.Set(raw); err == nil {
				t.Fatalf("expected %q to be rejected", raw)
			}
		})
	}
}
