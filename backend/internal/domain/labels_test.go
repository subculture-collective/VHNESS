package domain

import (
	"strings"
	"testing"
)

func TestSeriesProposalIssueFields(t *testing.T) {
	position := 3
	negative := -1
	series := LabelProposal{Item: "issue", Label: "riot", Method: "editor", IssuePosition: &position, IssueLabel: "Volume 3"}
	if err := series.Validate(); err != nil || series.CheckKind(SeriesKind) != nil {
		t.Fatal("valid series proposal", err)
	}
	if series.CheckKind("topic") == nil {
		t.Fatal("issue fields accepted on a topic label")
	}
	if (LabelProposal{Item: "issue", Label: "riot", Method: "editor"}).CheckKind(SeriesKind) == nil {
		t.Fatal("series proposal without an issue position accepted")
	}
	for _, invalid := range []LabelProposal{
		{Item: "issue", Label: "riot", Method: "editor", IssueLabel: "Volume 3"},
		{Item: "issue", Label: "riot", Method: "editor", IssuePosition: &position},
		{Item: "issue", Label: "riot", Method: "editor", IssuePosition: &position, IssueLabel: " "},
		{Item: "issue", Label: "riot", Method: "editor", IssuePosition: &negative, IssueLabel: "Volume 3"},
		{Item: "issue", Label: "riot", Method: "editor", IssuePosition: &position, IssueLabel: strings.Repeat("x", 201)},
	} {
		if invalid.Validate() == nil {
			t.Fatal("invalid issue fields accepted", invalid)
		}
	}
	if !LabelKinds[SeriesKind] || (Label{ID: "riot", Kind: SeriesKind, Label: "Riot Grrrl"}).Validate() != nil {
		t.Fatal("series label kind")
	}
}
