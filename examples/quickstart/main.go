package main

import (
	"context"
	"fmt"
	"log"

	"github.com/masterkeysrd/loom/graph"
)

type Greeting struct {
	Name string
	Text string
}

func (s Greeting) Copy() Greeting { return s }

func greet(_ context.Context, _ Greeting) (graph.Command[Greeting], error) {
	return graph.Update[Greeting](func(s Greeting) Greeting {
		s.Text = "Hello, " + s.Name + "!"
		return s
	}), nil
}

func main() {
	builder := graph.New[Greeting]()
	builder.AddNode("greet", graph.NodeFunc(greet))
	builder.AddEdge(graph.START, "greet")
	builder.AddEdge("greet", graph.END)
	g, err := builder.Build()
	if err != nil {
		log.Fatal(err)
	}

	input := graph.Update[Greeting](func(s Greeting) Greeting {
		s.Name = "Ada"
		return s
	})
	result, err := g.Execute(context.Background(), input, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.State.Text)
}
