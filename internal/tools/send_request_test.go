package tools

import (
	"reflect"
	"testing"
)

// Guard that the no-op fields removed in PR1 stay gone.
func TestSendRequestInput_NoFollowRedirectsOrSSLVerify(t *testing.T) {
	typ := reflect.TypeOf(SendRequestInput{})
	banned := []string{"FollowRedirects", "SSLVerify"}
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		for _, b := range banned {
			if name == b {
				t.Errorf("SendRequestInput still has removed field %q", name)
			}
		}
	}
}

func TestSendRequestInput_HasRequiredFields(t *testing.T) {
	typ := reflect.TypeOf(SendRequestInput{})
	required := []string{"Host", "Port", "TLS", "Raw"}
	fieldSet := make(map[string]bool)
	for i := range typ.NumField() {
		fieldSet[typ.Field(i).Name] = true
	}
	for _, name := range required {
		if !fieldSet[name] {
			t.Errorf("SendRequestInput missing field %q", name)
		}
	}
}

func TestSendRequestOutput_HasElapsedMs(t *testing.T) {
	typ := reflect.TypeOf(SendRequestOutput{})
	fields := make(map[string]string)
	for i := range typ.NumField() {
		f := typ.Field(i)
		fields[f.Name] = f.Tag.Get("json")
	}
	if tag, ok := fields["ElapsedMs"]; !ok || tag != "elapsed_ms,omitempty" {
		t.Errorf("SendRequestOutput.ElapsedMs json tag: want %q, got %q", "elapsed_ms,omitempty", tag)
	}
}
