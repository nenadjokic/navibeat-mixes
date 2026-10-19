package config

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// Navidrome draws the plugin settings form from config.uiSchema, not from
// config.schema. A property that is in the schema but has no Control in the
// uiSchema is read by the plugin and never shown to anyone, so nobody can set
// it. That is how the 0.9.13 libraries setting shipped: in the schema, in the
// code, in the README, and absent from the form (navibeat-mixes#8).
func TestEverySettingHasAControlInTheForm(t *testing.T) {
	raw, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Config struct {
			Schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schema"`
			UISchema json.RawMessage `json:"uiSchema"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var ui any
	if err := json.Unmarshal(m.Config.UISchema, &ui); err != nil {
		t.Fatal(err)
	}
	shown := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if s, ok := x["scope"].(string); ok {
				const p = "#/properties/"
				if len(s) > len(p) && s[:len(p)] == p {
					shown[s[len(p):]] = true
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(ui)
	var missing []string
	for name := range m.Config.Schema.Properties {
		if !shown[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("settings with no control in the form, so no user can set them: %v", missing)
	}
}

// manifestForm is the part of manifest.json the tests below read.
type manifestForm struct {
	Config struct {
		Schema struct {
			Properties map[string]struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				Default     any    `json:"default"`
			} `json:"properties"`
		} `json:"schema"`
		UISchema struct {
			Elements []struct {
				Label    string `json:"label"`
				Elements []struct {
					Scope string `json:"scope"`
				} `json:"elements"`
			} `json:"elements"`
		} `json:"uiSchema"`
	} `json:"config"`
}

func readManifestForm(t *testing.T) (manifestForm, func(group string) map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifestForm
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m, func(group string) map[string]bool {
		in := map[string]bool{}
		for _, g := range m.Config.UISchema.Elements {
			if g.Label == group {
				for _, e := range g.Elements {
					in[strings.TrimPrefix(e.Scope, "#/properties/")] = true
				}
			}
		}
		return in
	}
}

// Every name template the code reads (templateFamilies) and every slot name
// (nameSlots) is declared and in the "Names" group, in both directions. The
// templates also have to name the placeholder, stay short, and never call
// themselves a "template" in the title.
func TestNameSettingsAreDeclaredAndGrouped(t *testing.T) {
	m, group := readManifestForm(t)
	inGroup := group("Names")
	want := map[string]bool{}
	for _, s := range nameSlots {
		want["name"+s.suffix] = false
	}
	for _, f := range templateFamilies {
		want[f.field] = true
	}
	for k, isTemplate := range want {
		p, ok := m.Config.Schema.Properties[k]
		if !ok || p.Title == "" {
			t.Errorf("%s: missing from the manifest or has no title", k)
			continue
		}
		if !inGroup[k] {
			t.Errorf("%s is not in the Names group", k)
		}
		if !strings.HasPrefix(p.Title, "Name: ") {
			t.Errorf("%s: title %q must start with \"Name: \"", k, p.Title)
		}
		if strings.Contains(strings.ToLower(p.Title), "template") {
			t.Errorf("%s: title %q must not say template", k, p.Title)
		}
		if !isTemplate {
			continue
		}
		if p.Default != "" || p.Description == "" {
			t.Errorf("%s: want an empty default and a description", k)
		}
		if !strings.Contains(p.Description, "{subject}") {
			t.Errorf("%s: description lacks {subject}", k)
		}
		if n := utf8.RuneCountInString(p.Description); n > 120 {
			t.Errorf("%s: description is %d characters, want at most 120", k, n)
		}
	}
	for k := range m.Config.Schema.Properties {
		if strings.HasPrefix(k, "name") {
			if _, ok := want[k]; !ok {
				t.Errorf("%s is in the manifest but not in the code's name tables", k)
			}
		}
	}
	for k := range inGroup {
		if _, ok := want[k]; !ok {
			t.Errorf("%s is in the Names group but not read by the code", k)
		}
	}
}
