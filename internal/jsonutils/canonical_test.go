package jsonutils

import (
	"encoding/json"
	"testing"
)

type sampleStruct struct {
	Zebra string `json:"zebra"`
	Apple string `json:"apple"`
	Count int64  `json:"count"`
}

func TestCanonical(t *testing.T) {
	s := sampleStruct{
		Zebra: "stripes",
		Apple: "fruit",
		Count: 42,
	}

	canonBytes, err := Canonical(s)
	if err != nil {
		t.Fatalf("Canonical failed: %v", err)
	}

	expected := `{"apple":"fruit","count":42,"zebra":"stripes"}`
	if string(canonBytes) != expected {
		t.Errorf("expected %s, got %s", expected, string(canonBytes))
	}

	// Verify that unmarshaled map produces the exact same canonical JSON
	var m any
	if err := json.Unmarshal(canonBytes, &m); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	mapCanonBytes, err := Canonical(m)
	if err != nil {
		t.Fatalf("Canonical on map failed: %v", err)
	}

	if string(canonBytes) != string(mapCanonBytes) {
		t.Errorf("struct canonical (%s) != map canonical (%s)", string(canonBytes), string(mapCanonBytes))
	}
}
