package main

// seeder/validator/main.go
// Validates catalog-info.yaml structure before commit.
// Usage: go run seeder/validator/main.go <catalog-file>
// Exit 0 = valid, Exit 1 = hard errors

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Entity struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Identifier string                 `yaml:"identifier"`
	Name       string                 `yaml:"name"`
	Owner      string                 `yaml:"owner"`
	Metadata   EntityMetadata         `yaml:"metadata"`
	Spec       map[string]interface{} `yaml:"spec,omitempty"`
}

type EntityMetadata struct {
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: validator <catalog-file>")
	}
	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}

	var entities []*Entity
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var e Entity
		if err := dec.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("yaml parse error in %s: %v", path, err)
		}
		if e.APIVersion != "" {
			entities = append(entities, &e)
		}
	}

	var fails []string
	var warns []string

	identifiers := make(map[string]bool)

	// Duplicate identifier check
	for _, e := range entities {
		if e.Identifier == "" {
			fails = append(fails, fmt.Sprintf("entity kind=%s has empty identifier", e.Kind))
			continue
		}
		if identifiers[e.Identifier] {
			fails = append(fails, fmt.Sprintf("duplicate identifier %q", e.Identifier))
		}
		identifiers[e.Identifier] = true
	}

	// Component checks
	comp := findByKind(entities, "Component")
	if comp == nil {
		fails = append(fails, "no Component entity found")
	} else {
		if comp.Identifier == "" {
			fails = append(fails, "Component.identifier is empty")
		}
		if comp.Owner == "" {
			fails = append(fails, "Component.owner is empty")
		}
		for _, key := range []string{"bank.com/sysid", "github.com/project-slug"} {
			if comp.Metadata.Annotations[key] == "" {
				fails = append(fails, fmt.Sprintf("Component missing required annotation %q", key))
			}
		}
		if comp.Spec == nil || comp.Spec["system"] == nil {
			fails = append(fails, "Component.spec.system is empty")
		}

		// Cross-reference dependsOn against entities in this file
		for _, ref := range toStringSlice(comp.Spec["dependsOn"]) {
			id := extractID(ref)
			if id != "" && !identifiers[id] {
				warns = append(warns, fmt.Sprintf("dependsOn %q has no matching entity in file (may be defined elsewhere)", ref))
			}
		}
	}

	// apiVersion check for all entities
	for _, e := range entities {
		if e.APIVersion != "harness.io/v1" {
			fails = append(fails, fmt.Sprintf("entity %q has unexpected apiVersion %q", e.Identifier, e.APIVersion))
		}
	}

	for _, w := range warns {
		fmt.Printf("WARN: %s\n", w)
	}
	if len(fails) > 0 {
		for _, f := range fails {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", f)
		}
		os.Exit(1)
	}

	fmt.Printf("validator: %s OK (%d entities)\n", path, len(entities))
}

func findByKind(entities []*Entity, kind string) *Entity {
	for _, e := range entities {
		if e.Kind == kind {
			return e
		}
	}
	return nil
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	var out []string
	if sl, ok := v.([]interface{}); ok {
		for _, item := range sl {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func extractID(ref string) string {
	// "resource:account/payments_postgres" -> "payments_postgres"
	parts := strings.Split(ref, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
