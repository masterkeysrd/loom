package studio

import (
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// InspectSchema searches for []message.Message fields and adds semantic flags.
func InspectSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}

	for name, prop := range schema.Properties {
		// Recursive check
		InspectSchema(prop)

		ln := strings.ToLower(name)
		if (prop.Type == "array" || len(prop.Type) == 0) && (strings.Contains(ln, "message") || strings.Contains(ln, "history")) {
			// Add custom attribute for UI
			if prop.Extra == nil {
				prop.Extra = make(map[string]any)
			}
			prop.Extra["x-loom-content"] = "chat"
			prop.Extra["x-loom-type"] = "message_list"

			if prop.Items == nil {
				prop.Items = &jsonschema.Schema{}
			}
			prop.Items.Type = "object"
		}
	}
}
