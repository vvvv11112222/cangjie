package optional

import (
	"encoding/json"
	"testing"
)

func TestValueDistinguishesMissingNullAndValue(t *testing.T) {
	type input struct {
		Capacity Value[int] `json:"capacity"`
	}
	var missing, nullValue, actual input
	if err := json.Unmarshal([]byte(`{}`), &missing); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"capacity":null}`), &nullValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"capacity":42}`), &actual); err != nil {
		t.Fatal(err)
	}
	if missing.Capacity.Set || !nullValue.Capacity.Set || nullValue.Capacity.Value != nil || !actual.Capacity.Set || actual.Capacity.Value == nil || *actual.Capacity.Value != 42 {
		t.Fatalf("unexpected optional values: missing=%+v null=%+v actual=%+v", missing, nullValue, actual)
	}
}
