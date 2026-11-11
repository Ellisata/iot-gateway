// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingdsl

import "testing"

func TestValidateNormalizationAllowedOps(t *testing.T) {
	ts := []Transform{
		{Op: OpDefault, Value: "0"},
		{Op: OpScale, Factor: floatPtr(0.1)},
		{Op: OpRound, Digits: 2},
		{Op: OpTrim},
	}
	if err := ValidateNormalization(ts); err != nil {
		t.Fatalf("ValidateNormalization() = %v", err)
	}
}

func TestValidateNormalizationRejectsNonNormalizationOps(t *testing.T) {
	for _, op := range []string{OpHexDecode, OpBase64Decode, OpBitfield, OpRegex} {
		ts := []Transform{{Op: op, Pattern: "(\\d+)", Offset: floatPtr(0), Length: 8}}
		if err := ValidateNormalization(ts); err == nil {
			t.Errorf("op %q accepted, want rejection", op)
		}
	}
}

func TestValidateNormalizationRejectsTooManySteps(t *testing.T) {
	ts := make([]Transform, MaxNormalizationTransforms+1)
	for i := range ts {
		ts[i] = Transform{Op: OpTrim}
	}
	if err := ValidateNormalization(ts); err == nil {
		t.Fatal("over-long pipeline accepted")
	}
}

func TestValidateNormalizationDefaultMustBeFirst(t *testing.T) {
	ts := []Transform{
		{Op: OpScale, Factor: floatPtr(0.1)},
		{Op: OpDefault, Value: "0"},
	}
	if err := ValidateNormalization(ts); err == nil {
		t.Fatal("default after scale accepted, want rejection")
	}
}

func TestValidateNormalizationDefaultNeedsValue(t *testing.T) {
	if err := ValidateNormalization([]Transform{{Op: OpDefault}}); err == nil {
		t.Fatal("default without value accepted")
	}
}

func TestParseNormalization(t *testing.T) {
	if ts, err := ParseNormalization(""); err != nil || ts != nil {
		t.Fatalf("empty: ts=%v err=%v", ts, err)
	}
	if ts, err := ParseNormalization("null"); err != nil || ts != nil {
		t.Fatalf("null: ts=%v err=%v", ts, err)
	}

	ts, err := ParseNormalization(`[{"op":"scale","factor":0.1},{"op":"round","digits":2}]`)
	if err != nil {
		t.Fatalf("valid JSON: %v", err)
	}
	if len(ts) != 2 || ts[0].Op != OpScale || ts[0].Factor == nil || *ts[0].Factor != 0.1 {
		t.Fatalf("unexpected pipeline: %+v", ts)
	}

	if _, err := ParseNormalization(`{"op":"scale"}`); err == nil {
		t.Fatal("object accepted, want array")
	}
	if _, err := ParseNormalization(`[{"op":"regex","pattern":"x"}]`); err == nil {
		t.Fatal("regex accepted, want rejection")
	}
}

func TestNormalizationForcesFloat(t *testing.T) {
	cases := []struct {
		name string
		ts   []Transform
		want bool
	}{
		{"scale", []Transform{{Op: OpScale, Factor: floatPtr(1)}}, true},
		{"round decimals", []Transform{{Op: OpRound, Digits: 2}}, true},
		{"round integer", []Transform{{Op: OpRound, Digits: 0}}, false},
		{"trim", []Transform{{Op: OpTrim}}, false},
		{"default", []Transform{{Op: OpDefault, Value: "0"}}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizationForcesFloat(c.ts); got != c.want {
				t.Fatalf("NormalizationForcesFloat() = %v, want %v", got, c.want)
			}
		})
	}
}

func floatPtr(v float64) *float64 { return &v }
