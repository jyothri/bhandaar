package wire

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The golden fixtures in testdata are the contract: both agentserver and
// driveagent test against them. Each must decode strictly into its type and
// encode back to the same JSON.
func TestGoldenFixtures(t *testing.T) {
	cases := map[string]func() any{
		"changes-batch.json":       func() any { return new(ChangeBatch) },
		"changes-response.json":    func() any { return new(ChangesResponse) },
		"drive-open-request.json":  func() any { return new(DriveOpenRequest) },
		"drive-open-response.json": func() any { return new(DriveOpenResponse) },
	}
	for name, newV := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			v := newV()
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v); err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			json.Unmarshal(b, &want)
			json.Unmarshal(out, &got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("round trip differs:\nfixture: %s\nencoded: %s", b, out)
			}
		})
	}
}

// An empty drive-root parent must be sent, not dropped.
func TestEmptyPathIsSent(t *testing.T) {
	b, _ := json.Marshal(Change{V: 1, Kind: KindDirChild, Op: OpDelete, Path: Ptr(""), Child: Ptr("x")})
	if !bytes.Contains(b, []byte(`"path":""`)) {
		t.Errorf("encoded %s", b)
	}
}
