package graph

import (
	"encoding/base64"
	"fmt"
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
			fmt.Fprintf(&sb, "  %s((START))\n", START)
		case END:
			fmt.Fprintf(&sb, "  %s((END))\n", END)
		default:
			fmt.Fprintf(&sb, "  %s[%s]\n", name, name)
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
			if edge == nil {
				continue
			}
			for _, tr := range edge.Transitions() {
				switch {
				case tr.Conditional && tr.Label != "":
					fmt.Fprintf(&sb, "  %s -. [%s] .-> %s\n", from, tr.Label, tr.Target)
				case tr.Conditional:
					fmt.Fprintf(&sb, "  %s -.-> %s\n", from, tr.Target)
				case tr.Label != "":
					fmt.Fprintf(&sb, "  %s -- %s --> %s\n", from, tr.Label, tr.Target)
				default:
					fmt.Fprintf(&sb, "  %s --> %s\n", from, tr.Target)
				}
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
