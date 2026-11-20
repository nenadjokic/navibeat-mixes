package config

import (
	"reflect"
	"testing"
)

// navibeat-mixes#20, dcihon after 0.9.31: one account may want only the 60s,
// another the 80s and 90s. "user: decades" parts, separated by ";", apply to
// that account only and replace the shared list for it; a part with no name is
// the shared list for everyone else. A value with no ";" reads exactly as in
// 0.9.31.
func TestDecadesPerUser(t *testing.T) {
	c := Load(func(k string) string {
		return map[string]string{
			"decadesInclude": "1970s; nenad: 1960s; Ana: 1980s, 1990s",
			"decadesExclude": "2020s; ana: 2010s",
		}[k]
	})
	for _, tc := range []struct {
		user          string
		include, excl []int
	}{
		{"nenad", []int{1960}, []int{2020}},
		{"ana", []int{1980, 1990}, []int{2010}},
		{"ANA", []int{1980, 1990}, []int{2010}},
		{"marko", []int{1970}, []int{2020}},
	} {
		if got := c.DecadesIncludeFor(tc.user); !reflect.DeepEqual(got, tc.include) {
			t.Errorf("DecadesIncludeFor(%q) = %v, want %v", tc.user, got, tc.include)
		}
		if got := c.DecadesExcludeFor(tc.user); !reflect.DeepEqual(got, tc.excl) {
			t.Errorf("DecadesExcludeFor(%q) = %v, want %v", tc.user, got, tc.excl)
		}
	}
}

func TestDecadesWithoutUsersReadAsBefore(t *testing.T) {
	c := Load(func(k string) string {
		return map[string]string{"decadesInclude": "1960s, 1970"}[k]
	})
	if !reflect.DeepEqual(c.DecadesInclude, []int{1960, 1970}) {
		t.Errorf("DecadesInclude = %v", c.DecadesInclude)
	}
	if got := c.DecadesIncludeFor("anyone"); !reflect.DeepEqual(got, []int{1960, 1970}) {
		t.Errorf("DecadesIncludeFor = %v", got)
	}
	if got := c.DecadesExcludeFor("anyone"); got != nil {
		t.Errorf("DecadesExcludeFor = %v, want nil", got)
	}
}

func TestOnlyUserPartsLeaveOthersAutomatic(t *testing.T) {
	c := Load(func(k string) string {
		return map[string]string{"decadesInclude": "nenad: 1960s"}[k]
	})
	if got := c.DecadesIncludeFor("marko"); got != nil {
		t.Errorf("DecadesIncludeFor(marko) = %v, want nil (automatic)", got)
	}
}
