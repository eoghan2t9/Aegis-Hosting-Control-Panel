package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// A plain json decode turns every number into a float64, which rounds integers
// above 2^53: a BIGINT key or value written through the editor would come back
// changed. decodeExact keeps the original digits.
func TestDecodeExactKeepsBigIntegers(t *testing.T) {
	const body = `{"key":{"id":12345678901234567},"values":{"n":9007199254740993,"price":12.50,"name":"x","gone":null,"ok":true}}`

	var exact struct {
		Key    map[string]any `json:"key"`
		Values map[string]any `json:"values"`
	}
	if err := decodeExact(strings.NewReader(body), &exact); err != nil {
		t.Fatal(err)
	}
	if got := exact.Key["id"]; got != json.Number("12345678901234567") {
		t.Errorf("key id = %#v, want the exact digits", got)
	}
	if got := exact.Values["n"]; got != json.Number("9007199254740993") {
		t.Errorf("value n = %#v, want the exact digits", got)
	}
	if exact.Values["price"] != json.Number("12.50") || exact.Values["name"] != "x" || exact.Values["gone"] != nil || exact.Values["ok"] != true {
		t.Errorf("other values changed: %#v", exact.Values)
	}

	// The failure this prevents: the ordinary decoder rounds the same digits.
	var lossy map[string]any
	if err := json.Unmarshal([]byte(`{"id":12345678901234567}`), &lossy); err != nil {
		t.Fatal(err)
	}
	if f, ok := lossy["id"].(float64); !ok || f == 12345678901234567 {
		t.Log("(this platform's float64 happened to round-trip it)")
	}
}
