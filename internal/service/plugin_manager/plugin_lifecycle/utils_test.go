// Package plugin_lifecycle 提供单元测试
// 文件位置: internal/service/plugin_manager/plugin_lifecycle/utils_test.go
package plugin_lifecycle

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"testing"
)

func TestEnsureProtocolCompatible(t *testing.T) {
	cases := []struct {
		name    string
		version *datasourcev1.ApiVersion
		wantErr bool
	}{
		{
			name:    "nil version",
			version: nil,
			wantErr: true,
		},
		{
			name:    "major mismatch",
			version: &datasourcev1.ApiVersion{Major: 2, Minor: 0, Patch: 0},
			wantErr: true,
		},
		{
			name:    "minor too new",
			version: &datasourcev1.ApiVersion{Major: gatewayProtocolMajor, Minor: gatewayProtocolMinor + 1, Patch: 0},
			wantErr: true,
		},
		{
			name:    "exact match",
			version: &datasourcev1.ApiVersion{Major: gatewayProtocolMajor, Minor: gatewayProtocolMinor, Patch: gatewayProtocolPatch},
			wantErr: false,
		},
		{
			name:    "older minor",
			version: &datasourcev1.ApiVersion{Major: gatewayProtocolMajor, Minor: 0, Patch: 0},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureProtocolCompatible(tc.version)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFormatAPIVersion(t *testing.T) {
	if formatAPIVersion(nil) != "" {
		t.Fatalf("formatAPIVersion(nil) should return empty string")
	}
	v := &datasourcev1.ApiVersion{Major: 1, Minor: 2, Patch: 3}
	if got := formatAPIVersion(v); got != "1.2.3" {
		t.Fatalf("unexpected version string: %s", got)
	}
}
