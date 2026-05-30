package graph

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ToMermaid generates a Mermaid-formatted string representing the graph's structure.
// This string can be rendered in Mermaid-compatible viewers or README files.
func (g *Graph[S]) ToMermaid() string {
	var sb strings.Builder
	sb.WriteString("graph TD\n")

	// 1. Define nodes and their shapes
	// Sort node names for deterministic output
	nodeNames := make([]string, 0, len(g.nodes))
	for name := range g.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		switch name {
		case START:
			sb.WriteString(fmt.Sprintf("  %s((START))\n", START))
		case END:
			sb.WriteString(fmt.Sprintf("  %s((END))\n", END))
		default:
			sb.WriteString(fmt.Sprintf("  %s[%s]\n", name, name))
		}
	}

	// 2. Define edges
	// Sort 'from' nodes for deterministic output
	fromNodes := make([]string, 0, len(g.edges))
	for from := range g.edges {
		fromNodes = append(fromNodes, from)
	}
	sort.Strings(fromNodes)

	for _, from := range fromNodes {
		edges := g.edges[from]
		for _, edge := range edges {
			// Use reflection to inspect the underlying edge type
			v := reflect.ValueOf(edge)
			for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
				v = v.Elem()
			}

			// Use the type string to match generic types (e.g., SimpleEdge[...])
			typeStr := v.Type().String()
			
			if strings.Contains(typeStr, "SimpleEdge") {
				next := v.FieldByName("Next").String()
				sb.WriteString(fmt.Sprintf("  %s --> %s\n", from, next))
			} else if strings.Contains(typeStr, "ConditionalEdge") {
				next := v.FieldByName("Next").String()
				sb.WriteString(fmt.Sprintf("  %s -. [conditional] .-> %s\n", from, next))
			} else if strings.Contains(typeStr, "RouteEdge") {
				routes := v.FieldByName("Routes")
				if routes.Kind() == reflect.Map {
					// Extract and sort keys for deterministic output
					keys := routes.MapKeys()
					sort.Slice(keys, func(i, j int) bool {
						return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface())
					})

					for _, key := range keys {
						to := routes.MapIndex(key).String()
						sb.WriteString(fmt.Sprintf("  %s -- %v --> %s\n", from, key.Interface(), to))
					}
				}
			} else {
				// Fallback for custom edge types
				sb.WriteString(fmt.Sprintf("  %s -- unknown edge --> ???\n", from))
			}
		}
	}

	return sb.String()
}

// MermaidURL returns a link to render the graph diagram via mermaid.ink.
func (g *Graph[S]) MermaidURL() string {
	code := g.ToMermaid()
	// mermaid.ink uses a compressed/encoded format for long diagrams,
	// but standard base64 often works for simpler ones.
	encoded := base64.StdEncoding.EncodeToString([]byte(code))
	return fmt.Sprintf("https://mermaid.ink/img/%s", encoded)
}
