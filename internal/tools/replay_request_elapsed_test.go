package tools

import (
	"reflect"
	"testing"
)

func TestReplayRequestOutput_ElapsedMsAndRoundtripMs(t *testing.T) {
	typ := reflect.TypeOf(ReplayRequestOutput{})
	fields := make(map[string]string)
	for i := range typ.NumField() {
		f := typ.Field(i)
		fields[f.Name] = f.Tag.Get("json")
	}
	if tag, ok := fields["ElapsedMs"]; !ok || tag != "elapsed_ms,omitempty" {
		t.Errorf("ElapsedMs json tag: want %q, got %q", "elapsed_ms,omitempty", tag)
	}
	if tag, ok := fields["RoundtripMs"]; !ok || tag != "roundtripMs,omitempty" {
		t.Errorf("RoundtripMs json tag: want %q, got %q (deprecated alias must remain)", "roundtripMs,omitempty", tag)
	}
}
