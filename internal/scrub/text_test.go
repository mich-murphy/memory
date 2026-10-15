package scrub

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestTextWithholdsLiteralAssignmentsWithoutReportingValues(t *testing.T) {
	for name, line := range map[string]string{
		"atLimit":               "API_KEY=fixture-secret\n" + strings.Repeat("x", MaxTextSize-len("API_KEY=fixture-secret\n")),
		"fishHashLiteral":       "set -gx PASSWORD '#fixture-secret'",
		"shellSeparator":        "OTHER=ok;API_KEY=fixture-secret",
		"fishSeparator":         "set -gx OTHER ok;set -gx API_KEY fixture-secret",
		"fishAnd":               "set -gx OTHER ok;and set -gx API_KEY fixture-secret",
		"conditional":           "OTHER=ok&&API_KEY=fixture-secret",
		"pipeline":              "OTHER=ok|API_KEY=fixture-secret",
		"malformedTrailer":      "OTHER=ok API_KEY=fixture-secret CMD='",
		"malformedSecret":       "OTHER=ok API_KEY='fixture-secret",
		"singleQuotedReference": "API_KEY='${TOKEN}'",
		"escapedReference":      `API_KEY=\$TOKEN`,
		"quotedEscape":          `API_KEY="\$TOKEN"`,
		"fishSingleQuoted":      "set -gx API_KEY '${TOKEN}'",
		"fishEscaped":           `set -gx API_KEY \$TOKEN`,
		"fishMalformed":         "set -gx API_KEY 'fixture-secret",
		"mixedReference":        `API_KEY="$TOKEN"'${TOKEN}'`,
		"trailingEscape":        `API_KEY=\`,
		"hashInLiteral":         "API_KEY='fixture # private'",
		"shell":                 "API_KEY=fixture-private-value",
		"export":                "export\tGITHUB_TOKEN='fixture-private-value'",
		"fish":                  "set -gx API_KEY fixture-private-value",
		"fishQuoted":            "set\t-gx\tAPI_KEY \"fixture private value\"",
		"multiple":              "OTHER=ok PASSWORD=fixture-private-value command",
		"default":               "export API_KEY=${TOKEN:-fixture-private-value}",
		"unterminated":          "API_KEY='fixture-private-value",
		"fencedExample":         "```sh\nAPI_KEY=example-literal\n```",
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
		"quotedSeparator":     "OTHER='ok;API_KEY=fixture-secret'",
		"escapedSeparator":    `OTHER=ok\;API_KEY=fixture-secret`,
		"nonAssignmentArgs":   "echo API_KEY=fixture-secret",
		"separatedReferences": "OTHER=ok;API_KEY=$TOKEN;set -gx API_KEY \"${TOKEN}\"",
		"partialOther":        "API_KEY=$TOKEN OTHER='",
		"partialNonsecret":    "OTHER='",
		"quotedSemicolon":     "echo 'OTHER=ok;API_KEY=fixture-secret'",
		"quotedAssignment":    "'API_KEY=fixture-secret'",
		"literalHash":         "set -gx OTHER '#fixture-secret'",
		"fishEmpty":           "set -gx PASSWORD",
		"fishNonsecret":       "set -gx OTHER fixture-secret",
		"fishRedacted":        "set -gx PASSWORD '[redacted]'",
		"doubleQuoteEscape":   `API_KEY="[redacted]" OTHER="a\qb"`,
		"reference":           "API_KEY=$TOKEN",
		"quotedReference":     "export API_KEY=\"Bearer ${TOKEN}\"",
		"commentQuote":        "API_KEY=$TOKEN # it's a reference",
		"hashLiteral":         "OTHER='a # b' API_KEY=$TOKEN",
		"fishReference":       "set -gx API_KEY $TOKEN # reference",
		"redacted":            "API_KEY='[redacted]'",
		"empty":               "API_KEY=",
		"numeric":             "TOKEN=1234",
		"boolean":             "AUTH=false",
		"nonsecretName":       "MAX_TOKENS=2000\nKEYBOARD_LAYOUT=us\nMONKEY=animal",
		"commentExample":      "# API_KEY=example-literal",
		"proseExample":        "For example, export API_KEY=example-literal",
		"inlineExample":       "`API_KEY=example-literal`",
		"echo":                "echo 'API_KEY=example-literal'",
		"binary":              "API_KEY=fixture\x00",
		"invalidUTF8":         "API_KEY=fixture\xff",
		"large":               "API_KEY=fixture\n" + strings.Repeat("x", MaxTextSize),
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
