package autoping

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestSchemaVersion = 1

var ErrInvalidManifest = errors.New("invalid plugin manifest")

// Manifest is the canonical Plugin Manifest: identity, release metadata,
// configuration field descriptors, and default runtime configuration.
type Manifest struct {
	SchemaVersion int              `yaml:"schema_version"`
	ID            string           `yaml:"id"`
	Metadata      ManifestMetadata `yaml:"metadata"`
	Defaults      Config           `yaml:"defaults"`
}

type ManifestMetadata struct {
	Name             string          `yaml:"name"`
	Version          string          `yaml:"version"`
	Author           string          `yaml:"author"`
	GitHubRepository string          `yaml:"github_repository"`
	Description      string          `yaml:"description"`
	ConfigFields     []ManifestField `yaml:"config_fields"`
}

type ManifestField struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	EnumValues  []string `yaml:"enum_values,omitempty"`
	Description string   `yaml:"description"`
}

type manifestDocument struct {
	SchemaVersion *int             `yaml:"schema_version"`
	ID            string           `yaml:"id"`
	Metadata      ManifestMetadata `yaml:"metadata"`
	Defaults      rawConfig        `yaml:"defaults"`
}

var manifestFieldTypes = map[string]string{
	"auto_ping_disabled":        "boolean",
	"schedule":                 "array",
	"timezone":                 "string",
	"retry_cooldown":           "string",
	"max_concurrency":          "integer",
	"request_timeout":          "string",
	"prompt":                   "string",
	"model":                    "string",
	"model_candidates":         "array",
	"transport":                "enum",
	"scheduler_boost_fallback": "boolean",
	"exclude_credentials":      "array",
	"state_path":               "string",
}

// ParseManifest strictly decodes and validates a Plugin Manifest document.
// Unknown keys, missing metadata, incomplete field descriptors, and invalid
// defaults are rejected; there is no fallback.
func ParseManifest(data []byte) (Manifest, error) {
	var document manifestDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode YAML: %v", ErrInvalidManifest, err)
	}
	if document.SchemaVersion == nil {
		return Manifest{}, fmt.Errorf("%w: schema_version is required", ErrInvalidManifest)
	}
	if *document.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidManifest, *document.SchemaVersion)
	}
	if strings.TrimSpace(document.ID) == "" {
		return Manifest{}, fmt.Errorf("%w: id must not be empty", ErrInvalidManifest)
	}
	for _, required := range []struct{ field, value string }{
		{"name", document.Metadata.Name},
		{"version", document.Metadata.Version},
		{"author", document.Metadata.Author},
		{"github_repository", document.Metadata.GitHubRepository},
		{"description", document.Metadata.Description},
	} {
		if strings.TrimSpace(required.value) == "" {
			return Manifest{}, fmt.Errorf("%w: metadata %s must not be empty", ErrInvalidManifest, required.field)
		}
	}
	fields, err := parseManifestFields(document.Metadata.ConfigFields)
	if err != nil {
		return Manifest{}, err
	}
	defaults, err := configFromRaw(document.Defaults, Config{}, true)
	if err != nil {
		return Manifest{}, err
	}
	document.Metadata.ConfigFields = fields
	return Manifest{
		SchemaVersion: *document.SchemaVersion,
		ID:            strings.TrimSpace(document.ID),
		Metadata:      document.Metadata,
		Defaults:      defaults,
	}, nil
}

func parseManifestFields(declared []ManifestField) ([]ManifestField, error) {
	fields := make([]ManifestField, 0, len(manifestFieldTypes))
	seen := make(map[string]bool, len(manifestFieldTypes))
	for _, declaredField := range declared {
		expectedType, supported := manifestFieldTypes[declaredField.Name]
		if !supported {
			return nil, fmt.Errorf("%w: unsupported config field %q", ErrInvalidManifest, declaredField.Name)
		}
		if seen[declaredField.Name] {
			return nil, fmt.Errorf("%w: duplicate config field %q", ErrInvalidManifest, declaredField.Name)
		}
		if declaredField.Type != expectedType {
			return nil, fmt.Errorf("%w: config field %q must have type %q, got %q", ErrInvalidManifest, declaredField.Name, expectedType, declaredField.Type)
		}
		if strings.TrimSpace(declaredField.Description) == "" {
			return nil, fmt.Errorf("%w: config field %q must have a description", ErrInvalidManifest, declaredField.Name)
		}
		if declaredField.Name == "transport" {
			if !slices.Equal(declaredField.EnumValues, []string{TransportDirectHTTP, TransportSchedulerBoost}) {
				return nil, fmt.Errorf("%w: config field transport must declare enum values [%s, %s]", ErrInvalidManifest, TransportDirectHTTP, TransportSchedulerBoost)
			}
		} else if len(declaredField.EnumValues) != 0 {
			return nil, fmt.Errorf("%w: config field %q must not declare enum values", ErrInvalidManifest, declaredField.Name)
		}
		seen[declaredField.Name] = true
		fields = append(fields, ManifestField{
			Name:        declaredField.Name,
			Type:        declaredField.Type,
			EnumValues:  slices.Clone(declaredField.EnumValues),
			Description: declaredField.Description,
		})
	}
	var missing []string
	for name := range manifestFieldTypes {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("%w: missing config fields: %s", ErrInvalidManifest, strings.Join(missing, ", "))
	}
	return fields, nil
}
