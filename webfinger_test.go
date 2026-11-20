/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"net"
	"testing"
)

func TestIsPublicAddrRefusesDocumentationRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		// Documentation ranges (RFC 5737)
		{"192.0.2.1", false},
		{"198.51.100.7", false},
		{"203.0.113.9", false},
		// Benchmarking range (RFC 2544)
		{"198.18.0.1", false},
		{"198.19.255.254", false},
		// The same ranges reached through an IPv4-mapped or 6to4 address
		{"::ffff:203.0.113.9", false},
		{"2002:c000:201::1", false},
		// Just outside the benchmarking range
		{"198.17.255.255", true},
		{"198.20.0.1", true},
		// Public addresses
		{"11.89.67.10", true},
		{"93.184.216.34", true},
		// Existing checks
		{"127.0.0.1", false},
		{"10.1.2.3", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tt.addr)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) = nil", tt.addr)
			}
			if got := isPublicAddr(ip); got != tt.want {
				t.Errorf("isPublicAddr(%s) = %t, want %t", tt.addr, got, tt.want)
			}
		})
	}
}
