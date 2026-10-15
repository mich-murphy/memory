package scrub

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestTextWithholdsLiteralAssignmentsWithoutReportingValues(t *testing.T) {
	for name, line := range map[string]string{
		"hashInLiteral": "API_KEY='fixture # private'",
		"shell":         "API_KEY=fixture-private-value",
		"export":        "export\tGITHUB_TOKEN='fixture-private-value'",
		"fish":          "set -gx API_KEY fixture-private-value",
		"fishQuoted":    "set\t-gx\tAPI_KEY \"fixture private value\"",
		"multiple":      "OTHER=ok PASSWORD=fixture-private-value command",
		"default":       "export API_KEY=${TOKEN:-fixture-private-value}",
		"unterminated":  "API_KEY='fixture-private-value",
		"fencedExample": "```sh\nAPI_KEY=example-literal\n```",
	} {
		t.Run(name, func(t *testing.T) {
			err := Text([]byte(line))
			if !errors.Is(err, ErrLiteralSecret) {
				t.Fatal("Text did not withhold literal assignment")
			}
			if err.Error() != "literal_secret_assignment" {
				t.Fatal("Text error exposed source details")
			}
		})
	}
}

func TestTextKeepsReferencesAndNonAssignments(t *testing.T) {
	for name, line := range map[string]string{
		"reference":       "API_KEY=$TOKEN",
		"quotedReference": "export API_KEY=\"Bearer ${TOKEN}\"",
		"commentQuote":    "API_KEY=$TOKEN # it's a reference",
		"hashLiteral":     "OTHER='a # b' API_KEY=$TOKEN",
		"fishReference":   "set -gx API_KEY $TOKEN # reference",
		"redacted":        "API_KEY='[redacted]'",
		"empty":           "API_KEY=",
		"numeric":         "TOKEN=1234",
		"boolean":         "AUTH=false",
		"nonsecretName":   "MAX_TOKENS=2000\nKEYBOARD_LAYOUT=us\nMONKEY=animal",
		"commentExample":  "# API_KEY=example-literal",
		"proseExample":    "For example, export API_KEY=example-literal",
		"inlineExample":   "`API_KEY=example-literal`",
		"echo":            "echo 'API_KEY=example-literal'",
		"binary":          "API_KEY=fixture\x00",
		"invalidUTF8":     "API_KEY=fixture\xff",
		"large":           "API_KEY=fixture\n" + strings.Repeat("x", MaxTextSize),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Text([]byte(line)); err != nil {
				t.Fatalf("Text = %v, want allowed", err)
			}
		})
	}
}

func FuzzText(f *testing.F) {
	for _, seed := range []string{"API_KEY=fixture", "export TOKEN=$TOKEN", "set -gx PASSWORD 'fixture'", "# API_KEY=example", "API_KEY='", "\xff"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		before := bytes.Clone(data)
		err := Text(data)
		if !bytes.Equal(data, before) {
			t.Fatal("Text mutated input")
		}
		if err != nil && !errors.Is(err, ErrLiteralSecret) {
			t.Fatal("Text returned an unsafe diagnostic")
		}
		if !errors.Is(err, Text(data)) {
			t.Fatal("Text is not deterministic")
		}
	})
}
