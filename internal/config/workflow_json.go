package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// UnmarshalJSON preserves the source workflow's strict schema when a resolved
// config is restored from a run artifact or database. In particular, encoding/json
// alone would erase explicit null and duplicate workflow fields before validation.
func (r *Resolved) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return invalid("$", "invalid resolved JSON document")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return invalid("$", "resolved configuration must be an object")
	}
	root := doc.Content[0]
	// encoding/json matches struct fields without regard to case and lets the
	// last duplicate win. Check every envelope key before either behavior can
	// hide a workflow or bypass its recursive source-schema check.
	fields := make(map[string]bool)
	typ := reflect.TypeFor[Resolved]()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "-" {
			fields[name] = true
		}
	}
	seen := make(map[string]bool)
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if !fields[key] {
			return invalid(key, "unknown field")
		}
		if seen[key] {
			return invalid(key, "duplicate object key")
		}
		seen[key] = true
		if key == "workflow" {
			if err := checkSchema(root.Content[i+1], reflect.TypeFor[Workflow](), "workflow"); err != nil {
				return err
			}
		}
	}
	type plain Resolved
	var decoded plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&decoded); err != nil {
		return err
	}
	*r = Resolved(decoded)
	return nil
}
