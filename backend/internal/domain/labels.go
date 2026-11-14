package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// LabelKinds are the typed vocabularies a catalog label can belong to.
var LabelKinds = map[string]bool{"topic": true, "place": true, "organization": true, "person": true, "event": true, SeriesKind: true}

// SeriesKind labels an ordered series. Its assignments carry an issue
// position and an issue label; assignments of other kinds carry neither.
const SeriesKind = "series"

var labelID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// Label is one controlled vocabulary entry.
type Label struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Label       string   `json:"label"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
}

func ValidLabelID(id string) bool { return labelID.MatchString(id) }

func (l Label) Validate() error {
	if !ValidLabelID(l.ID) || !LabelKinds[l.Kind] || strings.TrimSpace(l.Label) == "" || len(l.Label) > 200 || len(l.Description) > 4000 || len(l.Aliases) > 100 {
		return fmt.Errorf("invalid label %q", l.ID)
	}
	for _, alias := range l.Aliases {
		if strings.TrimSpace(alias) == "" || len(alias) > 200 {
			return fmt.Errorf("invalid alias for label %q", l.ID)
		}
	}
	return nil
}

// LabelProposal records why a method suggests a label for an item. Proposals
// never become public on their own; an operator review accepts or rejects them.
type LabelProposal struct {
	Item     string          `json:"item"`
	Label    string          `json:"label"`
	Method   string          `json:"method"`
	Score    float64         `json:"score"`
	Evidence json.RawMessage `json:"evidence"`
	// IssuePosition and IssueLabel are required for a series label and
	// forbidden for every other kind. Position orders the series' items.
	IssuePosition *int   `json:"issue_position,omitempty"`
	IssueLabel    string `json:"issue_label,omitempty"`
}

func (p LabelProposal) Validate() error {
	if !ValidID(p.Item) || !ValidLabelID(p.Label) || strings.TrimSpace(p.Method) == "" || len(p.Method) > 200 || p.Score < 0 || p.Score > 1e6 {
		return fmt.Errorf("invalid label proposal for %q", p.Item)
	}
	if p.IssuePosition == nil && p.IssueLabel != "" || p.IssuePosition != nil && (*p.IssuePosition < 0 || *p.IssuePosition > math.MaxInt32 || strings.TrimSpace(p.IssueLabel) == "" || len(p.IssueLabel) > 200) {
		return fmt.Errorf("invalid issue position or label for %q", p.Item)
	}
	if len(p.Evidence) > 0 && (!json.Valid(p.Evidence) || len(p.Evidence) > 64<<10) {
		return fmt.Errorf("invalid label evidence for %q", p.Item)
	}
	return nil
}

// LabelDecision accepts or rejects an existing assignment.
type LabelDecision struct {
	Item  string `json:"item"`
	Label string `json:"label"`
	State string `json:"state"`
}

func (d LabelDecision) Validate() error {
	if !ValidID(d.Item) || !ValidLabelID(d.Label) || (d.State != "accepted" && d.State != "rejected" && d.State != "proposed") {
		return fmt.Errorf("invalid label decision for %q", d.Item)
	}
	return nil
}

// CheckKind reports whether the proposal's issue fields match the label kind.
func (p LabelProposal) CheckKind(kind string) error {
	if (kind == SeriesKind) != (p.IssuePosition != nil) {
		return fmt.Errorf("label %q: a series assignment requires an issue position and label; other kinds take neither", p.Label)
	}
	return nil
}

// ItemLabel is an accepted label as shown on a catalog item. Series labels
// include the item's issue position and issue label.
type ItemLabel struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	IssuePosition *int   `json:"issue_position,omitempty"`
	IssueLabel    string `json:"issue_label,omitempty"`
}

// TextHit is a matching page from archive-wide text search.
type TextHit struct {
	Representation string `json:"representation"`
	Page           int    `json:"page"`
	Snippet        string `json:"snippet"`
}
