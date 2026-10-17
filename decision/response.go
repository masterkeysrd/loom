package decision

import (
	"math"

	"github.com/masterkeysrd/loom/message"
)

// Response encapsulates the evaluation results of a decision model.
type Response struct {
	Model   string
	Answers map[string]Answer
	Metrics *message.TokenMetrics
}

// Answer looks up an Answer by the question name.
func (r *Response) Answer(name string) (Answer, bool) {
	if r == nil || r.Answers == nil {
		return Answer{}, false
	}
	ans, ok := r.Answers[name]
	return ans, ok
}

// OptionProbability holds the probability assigned to a specific choice option.
type OptionProbability struct {
	Value       string  `json:"value"`
	Probability float64 `json:"probability"`
}

// LevelProbability holds the probability assigned to a specific score rating level.
type LevelProbability struct {
	Index       int     `json:"index"`
	Label       string  `json:"label"`
	Probability float64 `json:"probability"`
}

// Answer represents the calibrated output for a single evaluated Question.
type Answer struct {
	Name                string              `json:"name"`
	Type                QuestionType        `json:"type"`
	Refused             bool                `json:"refused,omitempty"`
	Confidence          float64             `json:"confidence,omitempty"`
	Probability         float64             `json:"probability,omitempty"`
	Choice              string              `json:"choice,omitempty"`
	ChoiceProbabilities []OptionProbability `json:"choice_probabilities,omitempty"`
	Score               float64             `json:"score,omitempty"`
	ScoreProbabilities  []LevelProbability  `json:"score_probabilities,omitempty"`
}

// Passed returns true if the answer was not refused and its probability is greater than
// or equal to the specified threshold.
func (a Answer) Passed(threshold float64) bool {
	return !a.Refused && a.Probability >= threshold
}

// Prob returns the probability for a given choice value, or 0 if the value is not present.
func (a Answer) Prob(value string) float64 {
	for _, cp := range a.ChoiceProbabilities {
		if cp.Value == value {
			return cp.Probability
		}
	}
	return 0
}

// NormalizedScore returns the expected score normalized to the range [0.0, 1.0].
// If there are 1 or fewer score levels, it returns 0.
func (a Answer) NormalizedScore() float64 {
	if len(a.ScoreProbabilities) <= 1 {
		return 0
	}
	return a.Score / float64(len(a.ScoreProbabilities)-1)
}

// TopLevel returns the LevelProbability with the highest probability.
func (a Answer) TopLevel() (LevelProbability, bool) {
	if len(a.ScoreProbabilities) == 0 {
		return LevelProbability{}, false
	}
	top := a.ScoreProbabilities[0]
	for _, lp := range a.ScoreProbabilities[1:] {
		if lp.Probability > top.Probability {
			top = lp
		}
	}
	return top, true
}

// ConfidenceFromProbabilities computes the normalized entropy concentration:
//
//	1 - H(p) / ln(N)
//
// where H(p) = -\sum p_i \ln(p_i) for p_i > 0, clamped to [0.0, 1.0].
// A uniform distribution yields 0.0 (maximum uncertainty), while a deterministic
// distribution yields 1.0 (complete certainty).
func ConfidenceFromProbabilities(probs []float64) float64 {
	n := len(probs)
	if n <= 1 {
		if n == 1 {
			return 1.0
		}
		return 0.0
	}

	var sum float64
	for _, p := range probs {
		if p > 0 {
			sum += p
		}
	}
	if sum <= 0 {
		return 0.0
	}

	var h float64
	for _, p := range probs {
		if p > 0 {
			pn := p / sum
			h -= pn * math.Log(pn)
		}
	}

	maxH := math.Log(float64(n))
	if maxH <= 0 {
		return 1.0
	}

	conf := 1.0 - (h / maxH)
	if conf < 0.0 {
		return 0.0
	}
	if conf > 1.0 {
		return 1.0
	}
	return conf
}
