/*
Package utils/yaml offers tiny reusable parsers for simple YAML/text assets
used by zz-ops loaders. It keeps file scanning logic out of command scripts.
*/
package utils

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func ParseSimpleMapYAML(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read yaml %s: %w", path, err)
	}
	out := map[string]string{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse yaml map %s: %w", path, err)
	}
	return out, nil
}

func ParseSimpleListYAML(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read yaml %s: %w", path, err)
	}
	var out []string
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse yaml list %s: %w", path, err)
	}
	clean := make([]string, 0, len(out))
	for _, v := range out {
		v = strings.TrimSpace(v)
		if v != "" {
			clean = append(clean, v)
		}
	}
	return clean, nil
}

func ParseFrontMatterYAML(path string, meta any, body any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read yaml %s: %w", path, err)
	}
	parts := strings.Split(string(raw), "---")
	if len(parts) < 3 {
		return fmt.Errorf("invalid frontmatter format in %s", path)
	}
	if err := yaml.Unmarshal([]byte(parts[1]), meta); err != nil {
		return fmt.Errorf("parse frontmatter %s: %w", path, err)
	}
	bodyYAML := strings.TrimSpace(strings.Join(parts[2:], "---"))
	if err := yaml.Unmarshal([]byte(bodyYAML), body); err != nil {
		return fmt.Errorf("parse body payload %s: %w", path, err)
	}
	return nil
}
