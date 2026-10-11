package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestItemDTORoundTrip(t *testing.T) {
	in := ItemDTO{
		ID:        "abc",
		Type:      "text",
		Title:     "note",
		Data:      []byte{1, 2, 3},
		Version:   4,
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ItemDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Type != in.Type || out.Version != in.Version {
		t.Fatalf("mismatch: %+v vs %+v", out, in)
	}
	if string(out.Data) != string(in.Data) {
		t.Fatalf("data mismatch")
	}
}

func TestErrorResponseJSON(t *testing.T) {
	raw, _ := json.Marshal(ErrorResponse{Error: "boom"})
	if string(raw) != `{"error":"boom"}` {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}
