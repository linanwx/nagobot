package config

import "testing"

func TestCompressionPercentsDefaultWhenUnset(t *testing.T) {
	c := DefaultConfig()
	if t2, t3 := c.GetCompressionPercents(); t2 != 50 || t3 != 70 {
		t.Fatalf("defaults = %d/%d, want 50/70", t2, t3)
	}
	c.Thread.Compression = &CompressionConfig{Tier3Percent: 80}
	if t2, t3 := c.GetCompressionPercents(); t2 != 50 || t3 != 80 {
		t.Fatalf("partial override = %d/%d, want 50/80", t2, t3)
	}
}

// The order Tier 2 < Tier 3 < trim is what keeps compression ahead of the
// trim; a config that breaks it must fail to load, not run.
func TestValidateCompressionRejectsBrokenOrder(t *testing.T) {
	cases := []struct {
		name string
		cc   *CompressionConfig
		ok   bool
	}{
		{"unset", nil, true},
		{"explicit defaults", &CompressionConfig{Tier2Percent: 50, Tier3Percent: 70}, true},
		{"tier2 equals tier3", &CompressionConfig{Tier2Percent: 70, Tier3Percent: 70}, false},
		{"tier2 above default tier3", &CompressionConfig{Tier2Percent: 75}, false},
		{"tier3 at the cap", &CompressionConfig{Tier3Percent: 85}, true},
		{"tier3 past the cap", &CompressionConfig{Tier3Percent: 90}, false},
		{"negative", &CompressionConfig{Tier2Percent: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Thread.Compression = tc.cc
			if err := c.validateCompression(); (err == nil) != tc.ok {
				t.Fatalf("validateCompression() err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
