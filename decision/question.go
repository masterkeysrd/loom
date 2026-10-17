package decision

import (
	"github.com/masterkeysrd/loom/message"
)

// Input represents the context and content to be evaluated by the decision model.
// It can contain arbitrary structured state or text, alongside optional multimodal images.
type Input struct {
	State  any
	Images []message.ImageContent
}

// Text creates an Input with a plain text string as its state.
func Text(s string) Input {
	return Input{
		State: s,
	}
}

// State creates an Input with an arbitrary structured state value.
func State(v any) Input {
	return Input{
		State: v,
	}
}

// WithImages returns a copy of Input with the provided images appended.
func (in Input) WithImages(imgs ...message.ImageContent) Input {
	in.Images = append(in.Images, imgs...)
	return in
}

// QuestionType represents the kind of decision question to evaluate.
type QuestionType string

const (
	// QuestionPredicate represents a binary yes/no decision.
	QuestionPredicate QuestionType = "predicate"
	// QuestionChoice represents a categorical selection among discrete options.
	QuestionChoice QuestionType = "choice"
	// QuestionScore represents an ordinal rating across ordered levels.
	QuestionScore QuestionType = "score"
)

// Option represents a choice alternative in a categorical decision question.
type Option struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

// Opt creates a new Option with the given value and description.
func Opt(value, description string) Option {
	return Option{
		Value:       value,
		Description: description,
	}
}

// Level represents a rating step or milestone in an ordinal score question.
type Level struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Lvl creates a new Level with the given label and description.
func Lvl(label, description string) Level {
	return Level{
		Label:       label,
		Description: description,
	}
}

// Question defines a decision question to be evaluated against an Input context.
type Question struct {
	Name             string       `json:"name"`
	Type             QuestionType `json:"type"`
	Instructions     string       `json:"instructions"`
	TrueDescription  string       `json:"true_description,omitempty"`
	FalseDescription string       `json:"false_description,omitempty"`
	Choices          []Option     `json:"choices,omitempty"`
	Levels           []Level      `json:"levels,omitempty"`
}

// Predicate creates a binary question evaluated as a boolean true/false predicate.
func Predicate(name, instructions string) Question {
	return Question{
		Name:         name,
		Type:         QuestionPredicate,
		Instructions: instructions,
	}
}

// WithCriteria specifies the conditions under which the predicate evaluates to true vs false.
func (q Question) WithCriteria(trueDesc, falseDesc string) Question {
	q.TrueDescription = trueDesc
	q.FalseDescription = falseDesc
	return q
}

// Choice creates a categorical question with multiple options.
func Choice(name, instructions string, choices ...Option) Question {
	return Question{
		Name:         name,
		Type:         QuestionChoice,
		Instructions: instructions,
		Choices:      choices,
	}
}

// Score creates an ordinal score question with multiple descriptive levels.
func Score(name, instructions string, levels ...Level) Question {
	return Question{
		Name:         name,
		Type:         QuestionScore,
		Instructions: instructions,
		Levels:       levels,
	}
}
