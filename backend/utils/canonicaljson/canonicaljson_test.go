// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package canonicaljson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type vector struct {
	Name      string          `json:"name"`
	CfgType   string          `json:"cfgType"`
	Input     json.RawMessage `json:"input"`
	Canonical string          `json:"canonical"`
	Masked    string          `json:"masked"`
	Hash      string          `json:"hash"`
}

type keepVector struct {
	Name     string          `json:"name"`
	Existing json.RawMessage `json:"existing"`
	Incoming json.RawMessage `json:"incoming"`
	Want     string          `json:"want"`
}

type vectorFile struct {
	HashVersion int          `json:"hashVersion"`
	Vectors     []vector     `json:"vectors"`
	KeepVectors []keepVector `json:"keepVectors"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vf vectorFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	return vf
}

// TestVectors 是跨仓契约：iot-gateway-admin/backend/utils/canonicaljson
// 必须使用逐字相同的 vectors.json，任何一端改了算法都会被这组用例拦下。
func TestVectors(t *testing.T) {
	vf := loadVectors(t)
	if vf.HashVersion != HashVersion {
		t.Fatalf("vectors hashVersion=%d, package=%d", vf.HashVersion, HashVersion)
	}
	if len(vf.Vectors) == 0 {
		t.Fatal("vectors.json has no vectors")
	}
	for _, tc := range vf.Vectors {
		t.Run(tc.Name, func(t *testing.T) {
			v, err := Decode(tc.Input)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, err := Canonicalize(v)
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			if string(got) != tc.Canonical {
				t.Fatalf("canonical = %s, want %s", got, tc.Canonical)
			}
			masked, err := Canonicalize(MaskSecrets(v))
			if err != nil {
				t.Fatalf("mask: %v", err)
			}
			if string(masked) != tc.Masked {
				t.Fatalf("masked = %s, want %s", masked, tc.Masked)
			}
			hash, err := HashWithType(tc.CfgType, v)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if hash != tc.Hash {
				t.Fatalf("hash = %s, want %s", hash, tc.Hash)
			}
		})
	}
}

func TestKeepMaskedVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, tc := range vf.KeepVectors {
		t.Run(tc.Name, func(t *testing.T) {
			existing, err := Decode(tc.Existing)
			if err != nil {
				t.Fatalf("decode existing: %v", err)
			}
			incoming, err := Decode(tc.Incoming)
			if err != nil {
				t.Fatalf("decode incoming: %v", err)
			}
			got, err := Canonicalize(KeepMasked(existing, incoming))
			if err != nil {
				t.Fatalf("keep masked: %v", err)
			}
			if string(got) != tc.Want {
				t.Fatalf("keep masked = %s, want %s", got, tc.Want)
			}
		})
	}
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{"password", "Password", "saslPassword", "client_secret", "accessToken", "apiKey", "authorization", "signing_key"}
	for _, k := range secret {
		if !IsSecretKey(k) {
			t.Fatalf("expected secret key: %s", k)
		}
	}
	notSecret := []string{"host", "url", "mapping", "key", "groupName", "user", "username"}
	for _, k := range notSecret {
		if IsSecretKey(k) {
			t.Fatalf("unexpected secret key: %s", k)
		}
	}
}
