//go:build page

package web

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

// Colours of the page.
const (
	validatorsRed   = "rgb(248, 81, 73)" // --red, an err value
	validatorsWhite = "rgb(255, 255, 255)"
	// Highlights: of a cross-highlighted element (.hl), of its source
	// (.hl-source, a country's .cc-hl), and of a name highlighted in text
	// (span.hl, span.hl-source).
	validatorsHighlight       = "rgba(88, 166, 255, 0.08)"
	validatorsHighlightSource = "rgba(88, 166, 255, 0.25)"
	validatorsSpanHighlight   = "rgba(88, 166, 255, 0.25)"
	validatorsSpanSource      = "rgba(88, 166, 255, 0.4)"
)

// validatorsEcho is a validator of the tests' own networks, beside the
// healthy network's.
var validatorsEcho = newPageValidator("echo", "echo", 10)

// validatorsFoxtrot is a validator of the tests' own networks, beside the
// healthy network's.
var validatorsFoxtrot = newPageValidator("foxtrot", "foxtrot", 10)

// validatorsPowered is v with power.
func validatorsPowered(v pageValidator, power int64) pageValidator {
	v.power = power
	return v
}

// validatorsSigning is a live window of 100 blocks in which a validator
// missed missed and voted delayMs from each commit's median time.
func validatorsSigning(missed, delayMs int) node.ValSigning {
	return node.ValSigning{
		Signed: 100 - missed, Eligible: 100, Missed: missed, SignedInARow: 100 - missed,
		Votes: 100 - missed, VoteDelayMs: delayMs,
	}
}

// validatorsHistorySince is when the block history of specs starts.
func validatorsHistorySince(specs []valSpec) string {
	return specHistory(specs)[0].Time.UTC().Format(time.RFC3339)
}

// validatorsSortSpecs are four validators, in the set order charlie, echo,
// alpha, bravo, that each column orders differently:
//
//	         VP  country  provider   missed  24h  delay   props
//	alpha    40  DE       Hetzner    0       2    +12     2 / 5
//	bravo    30  AU       OVH        5       0    +1500   1 / 10
//	charlie  20  FR       AMAZON-02  1       7    −300    1 / 4
//	echo     10  JP       Vultr      3       1    +600    0 / 5
//
// alpha, the source's own, holds 40 % of the power: losing it halts the
// chain. Of bravo's 5 misses, 2 are votes for another block; it also voted
// nil 3 times.
func validatorsSortSpecs() []valSpec {
	bravo := validatorsSigning(5, 1500)
	bravo.OtherBlock, bravo.Nil = 2, 3
	return []valSpec{
		{
			val: validatorsPowered(valCharlie, 20), node: validatorNode(valCharlie, "203.0.113.12", "FR", "AMAZON-02"),
			signing: validatorsSigning(1, -300), absent: 7, proposals: 4, missedProposals: 1,
		},
		{
			val: validatorsEcho, node: validatorNode(validatorsEcho, "203.0.113.15", "JP", "Vultr"),
			signing: validatorsSigning(3, 600), absent: 1, proposals: 5,
		},
		{
			val: validatorsPowered(valAlpha, 40), node: validatorNode(valAlpha, "203.0.113.10", "DE", "Hetzner"),
			signing: validatorsSigning(0, 12), absent: 2, proposals: 5, missedProposals: 2,
		},
		{
			val: validatorsPowered(valBravo, 30), node: validatorNode(valBravo, "203.0.113.11", "AU", "OVH"),
			signing: bravo, proposals: 10, missedProposals: 1,
		},
	}
}

// validatorsCell is the cell of column col of validator v's row of the
// Validators table.
func validatorsCell(v pageValidator, col int) string {
	return fmt.Sprintf("%s > td:nth-child(%d)", validatorEntryAt(desktop, v.address()), col)
}

// validatorsCardValue is the value of key on validator v's card.
func validatorsCardValue(v pageValidator, key string) string {
	return fmt.Sprintf(`%s .peer-card-k:text-is(%q) + .peer-card-v`, validatorEntryAt(phone, v.address()), key)
}

// validatorsExpectRows checks the names of the Validators table's rows, in
// order.
func validatorsExpectRows(e *pageEnv, names ...string) {
	e.T.Helper()
	e.must(e.expect("#validators-body tr > td:first-child a.val-link").ToHaveText(names))
}

// validatorsMissedRows are the popover rows of a validator's misses over the
// last 100 blocks.
func validatorsMissedRows(missed, otherBlock, nilVotes int) [][2]string {
	return [][2]string{
		{"missed", strconv.Itoa(missed)},
		{"absent", strconv.Itoa(missed - otherBlock)},
		{"other block", strconv.Itoa(otherBlock)},
		{"nil votes", fmt.Sprintf("%d (counted as signed)", nilVotes)},
	}
}

// validatorsExpectInfoRows checks the popover of the ⓘ in cell: no title, and
// want as its rows' labels and values.
func validatorsExpectInfoRows(e *pageEnv, cell string, want [][2]string) {
	e.T.Helper()
	if title, rows := popRows(e, cell+" .info-btn"); title != "" || !slices.Equal(rows, want) {
		e.T.Errorf("%s: ⓘ popover = %q %q, want no title and %q", cell, title, rows, want)
	}
}

// validatorsDeclaredRows are the rows of the Provider cell's ⓘ of a validator
// whose location d declares: declared by the operator, on d's day.
func validatorsDeclaredRows(d location.Declaration) [][2]string {
	return [][2]string{{"declared by the operator", d.At.UTC().Format(time.DateOnly)}}
}
