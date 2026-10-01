package graphql

import "encoding/json"

// Caido 0.57.0 marks several input objects with the GraphQL @oneOf
// directive, which requires exactly one field to be present. genqlient
// generates these as all-pointer structs without `omitempty`, so an unset
// sibling field marshals to `null` and the server rejects the input with
// "Oneof input objects requires have exactly one field".
//
// genqlient does not generate MarshalJSON for input types, so the
// hand-written marshalers below (which emit only non-nil fields) are safe
// across regeneration. Keep one per @oneOf input that the SDK sends.

// MarshalJSON emits only the set field of this @oneOf input.
func (v RequestSourceInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if v.Id != nil {
		m["id"] = v.Id
	}
	if v.Raw != nil {
		m["raw"] = v.Raw
	}
	return json.Marshal(m)
}

// MarshalJSON emits only the set field of this @oneOf input.
func (v UpdateReplayEntryDraftInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if v.Http != nil {
		m["http"] = v.Http
	}
	if v.Ws != nil {
		m["ws"] = v.Ws
	}
	return json.Marshal(m)
}

// MarshalJSON emits only the set field of this @oneOf input.
func (v ReplaySessionSettingsInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if v.Http != nil {
		m["http"] = v.Http
	}
	return json.Marshal(m)
}

// MarshalJSON emits only the set field of this @oneOf input.
func (v QueryInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if v.HTTPQL != nil {
		m["HTTPQL"] = v.HTTPQL
	}
	if v.StreamQL != nil {
		m["streamQL"] = v.StreamQL
	}
	return json.Marshal(m)
}
