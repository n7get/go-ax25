// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

package bbs

// Copyright (C) 2026 Robert Ambrose N7GET
// SPDX-License-Identifier: GPL-2.0-or-later

import "testing"

func TestBBSConfigSchemaKeysMatchConsts(t *testing.T) {
	constSet := make(map[string]bool, len(allBBSConfigKeys))
	for _, k := range allBBSConfigKeys {
		constSet[string(k)] = true
	}

	for _, p := range BBSConfigSchema {
		if !constSet[string(p.Key)] {
			t.Fatalf("schema key %q missing from allBBSConfigKeys", p.Key)
		}
	}

	schemaSet := make(map[string]bool, len(BBSConfigSchema))
	for _, p := range BBSConfigSchema {
		schemaSet[string(p.Key)] = true
	}
	for _, k := range allBBSConfigKeys {
		if !schemaSet[string(k)] {
			t.Fatalf("const key %q missing from BBSConfigSchema", k)
		}
	}
}

func TestBBSConfigSchema_NoDuplicateKeys(t *testing.T) {
	seen := make(map[string]bool, len(BBSConfigSchema))
	for _, p := range BBSConfigSchema {
		k := string(p.Key)
		if seen[k] {
			t.Fatalf("duplicate key in BBSConfigSchema: %q", k)
		}
		seen[k] = true
	}
}
