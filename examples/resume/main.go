package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/masterkeysrd/loom/checkpoint/sqlite"
	"github.com/masterkeysrd/loom/graph"
	_ "modernc.org/sqlite"
)

type Review struct {
	Draft    string `json:"draft"`
	Approved bool   `json:"approved"`
}

func (s Review) Copy() Review { return s }

func review(_ context.Context, s Review) (graph.Command[Review], error) {
	if !s.Approved {
		return graph.InterrupCmd[Review]{}, nil
	}
	return nil, nil
}

func run(ctx context.Context, database string, approve bool) (string, error) {
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return "", err
	}
	defer db.Close()
	cp, err := sqlite.NewCheckpointer(db)
	if err != nil {
		return "", err
	}

	builder := graph.New[Review]().WithCheckpointer(cp)
	builder.AddNode("review", graph.NodeFunc(review))
	builder.AddEdge(graph.START, "review")
	builder.AddEdge("review", graph.END)
	g, err := builder.Build()
	if err != nil {
		return "", err
	}

	location := graph.Location{ThreadID: "release-notes"}
	previous, err := g.Load(ctx, location)
	if err != nil && !errors.Is(err, graph.ErrCheckpointNotFound) {
		return "", err
	}
	var input graph.Update[Review]
	switch {
	case previous == nil && approve:
		return "", errors.New("start the review before approving it")
	case previous == nil:
		input = func(s Review) Review {
			s.Draft = "Ship the new search"
			return s
		}
	case previous.IsDone():
		return "Already approved: " + previous.State.Draft, nil
	case approve:
		input = func(s Review) Review {
			s.Approved = true
			return s
		}
	default:
		return "Awaiting approval: " + previous.State.Draft, nil
	}
	snapshot, err := g.Execute(ctx, input, &location)
	if err != nil {
		return "", err
	}
	if snapshot.IsDone() {
		return "Approved: " + snapshot.State.Draft, nil
	}
	return "Awaiting approval: " + snapshot.State.Draft, nil
}

func main() {
	database := flag.String("db", "review.db", "SQLite database path")
	approve := flag.Bool("approve", false, "approve the saved draft")
	flag.Parse()
	result, err := run(context.Background(), *database, *approve)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
