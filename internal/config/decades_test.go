package config

import (
	"reflect"
	"testing"
)

func TestParseDecades(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []int
	}{
		{"1960s, 1970s", []int{1960, 1970}},
		{"1960", []int{1960}},
		{"1965", []int{1960}},
		{"1960S", []int{1960}},
		{"60s, '70s, ’80s", []int{1960, 1970, 1980}},
		{"00s, 10s, 20s, 30s, 90s", []int{2000, 2010, 2020, 1930, 1990}},
		{"1920s", []int{1920}},
		{"1990s, 1990, 90s", []int{1990}},
		// Invalid entries are skipped, the valid ones around them kept.
		{"sixties, 1960s, 65s, 1890s, 3000, 196, , 1970s", []int{1960, 1970}},
		{"s, ', x1960", nil},
	} {
		if got := parseDecades(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseDecades(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDecadeSettingsLoad(t *testing.T) {
	c := Load(func(k string) string {
		return map[string]string{
			"decadesInclude": "1960s, 1970",
			"decadesExclude": " 2020s ",
		}[k]
	})
	if !reflect.DeepEqual(c.DecadesInclude, []int{1960, 1970}) {
		t.Errorf("DecadesInclude = %v", c.DecadesInclude)
	}
	if !reflect.DeepEqual(c.DecadesExclude, []int{2020}) {
		t.Errorf("DecadesExclude = %v", c.DecadesExclude)
	}
}

// Untouched settings must leave both lists empty, which DecadePool treats as
// the automatic ranking exactly.
func TestDecadeSettingsDefaultEmpty(t *testing.T) {
	c := Load(func(string) string { return "" })
	if len(c.DecadesInclude) != 0 || len(c.DecadesExclude) != 0 {
		t.Errorf("defaults: include %v exclude %v, want both empty", c.DecadesInclude, c.DecadesExclude)
	}
}
