package tools

import (
	"testing"
)

func TestGetHttpqlSchema_HasRequiredFields(t *testing.T) {
	handler := getHttpqlSchemaHandler(nil)
	_, out, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	required := []string{"req.url", "req.host", "req.method", "req.path", "req.raw", "req.body", "resp.status", "resp.raw", "resp.body"}
	fieldSet := make(map[string]bool, len(out.Fields))
	for _, f := range out.Fields {
		fieldSet[f.Field] = true
	}
	for _, name := range required {
		if !fieldSet[name] {
			t.Errorf("missing field %q in schema output", name)
		}
	}
}

func TestGetHttpqlSchema_Combinators(t *testing.T) {
	handler := getHttpqlSchemaHandler(nil)
	_, out, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combs := make(map[string]bool)
	for _, c := range out.Combinators {
		combs[c] = true
	}
	for _, want := range []string{"and", "or"} {
		if !combs[want] {
			t.Errorf("missing combinator %q", want)
		}
	}
}

func TestGetHttpqlSchema_HasExamples(t *testing.T) {
	handler := getHttpqlSchemaHandler(nil)
	_, out, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Examples) == 0 {
		t.Fatal("expected at least one example query")
	}
}

func TestGetHttpqlSchema_EachFieldHasOperators(t *testing.T) {
	handler := getHttpqlSchemaHandler(nil)
	_, out, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range out.Fields {
		if len(f.Operators) == 0 {
			t.Errorf("field %q has no operators", f.Field)
		}
		if f.Type == "" {
			t.Errorf("field %q has no type", f.Field)
		}
	}
}

func TestGetHttpqlSchema_RespStatusIsInteger(t *testing.T) {
	handler := getHttpqlSchemaHandler(nil)
	_, out, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range out.Fields {
		if f.Field == "resp.status" {
			if f.Type != "integer" {
				t.Errorf("resp.status type: want %q, got %q", "integer", f.Type)
			}
			return
		}
	}
	t.Fatal("resp.status field not found")
}

func TestGetHttpqlSchema_NilClientOK(t *testing.T) {
	// handler must work without a live client (static data)
	handler := getHttpqlSchemaHandler(nil)
	_, _, err := handler(nil, nil, GetHttpqlSchemaInput{})
	if err != nil {
		t.Fatalf("nil client caused error: %v", err)
	}
}

func BenchmarkGetHttpqlSchemaHandler(b *testing.B) {
	handler := getHttpqlSchemaHandler(nil)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = handler(nil, nil, GetHttpqlSchemaInput{})
	}
}
