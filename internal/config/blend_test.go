package config

import "testing"

func TestBlendPairsAreReadAndCleaned(t *testing.T) {
	c := Load(func(k string) string {
		if k == "blendPairs" {
			return " nenad+Ana, ana+nenad , marko, x+x, a+b+c, nenad + marko "
		}
		return ""
	})
	if len(c.BlendPairs) != 2 {
		t.Fatalf("pairs = %v", c.BlendPairs)
	}
	got := c.BlendPartners("NENAD")
	if len(got) != 2 || got[0] != "Ana" || got[1] != "marko" {
		t.Fatalf("partners of nenad = %v", got)
	}
	if p := c.BlendPartners("ana"); len(p) != 1 || p[0] != "nenad" {
		t.Fatalf("partners of ana = %v", p)
	}
	if p := c.BlendPartners("zoe"); len(p) != 0 {
		t.Fatalf("zoe has partners %v", p)
	}
}

func TestNoBlendPairsByDefault(t *testing.T) {
	if c := Load(func(string) string { return "" }); len(c.BlendPairs) != 0 {
		t.Fatalf("default pairs = %v", c.BlendPairs)
	}
}
