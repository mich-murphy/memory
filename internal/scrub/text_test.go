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
			err := Text(nil, []byte(line))
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
			if err := Text(nil, []byte(line)); err != nil {
				t.Fatalf("Text = %v, want allowed", err)
			}
		})
	}
}

func TestTextRecognizesEquivalentFishAssignments(t *testing.T) {
	for name, prefix := range map[string]string{
		"quotedFlags":     "set '-gx' 'API_KEY' ",
		"quotedLongFlags": `set '--global' "--export" API_KEY `,
		"quotedCommand":   "'set' -gx API_KEY ",
		"joinedCommand":   "s'et' -gx API_KEY ",
		"quotedExport":    `"export" API_KEY=`,
		"joinedExport":    "ex'port' API_KEY=",
		"quotedName":      "set -gx 'API_KEY' ",
		"doubleQuoted":    `set -gx "API_KEY" `,
		"joinedName":      "set -gx A'PI'_KEY ",
		"reordered":       "set -xg API_KEY ",
		"separate":        "set -g -x API_KEY ",
		"reverseFlags":    "set -x -g API_KEY ",
		"longFlags":       "set --global --export API_KEY ",
		"reverseLong":     "set --export --global API_KEY ",
		"mixedFlags":      "set -g --export API_KEY ",
		"flagTerminator":  "set -gx -- 'API_KEY' ",
		"andSet":          "true;and set -xg 'API_KEY' ",
		"orSet":           "false;or set --global --export API_KEY ",
		"andExport":       "true;and export API_KEY=",
		"orExport":        "false;or export API_KEY=",
	} {
		t.Run(name, func(t *testing.T) {
			for _, value := range []string{"fixture-fish-secret", "'${TOKEN}'", `\$TOKEN`, "'fixture-fish-secret"} {
				if err := Text(nil, []byte(prefix+value)); !errors.Is(err, ErrLiteralSecret) || err.Error() != "literal_secret_assignment" {
					t.Fatal("Text did not safely withhold literal assignment")
				}
			}
			for _, value := range []string{"$TOKEN", `"${TOKEN}"`, "'[redacted]'"} {
				if err := Text(nil, []byte(prefix+value)); err != nil {
					t.Fatalf("Text = %v, want allowed reference or redaction", err)
				}
			}
		})
	}
}

func TestTextDoesNotTreatUnrelatedSetCommandsAsAssignments(t *testing.T) {
	for name, line := range map[string]string{
		"quotedAnd":            "true;'and' set -gx API_KEY fixture-private-value",
		"quotedOr":             `false;"or" export API_KEY=fixture-private-value`,
		"quotedAssignmentName": "ex'port' 'API_KEY'=fixture-private-value",
		"quotedAssignmentWord": "ex'port' 'API_KEY=fixture-private-value'",
		"dynamicCommand":       `"${SET_COMMAND}" -gx API_KEY fixture-private-value`,
		"incompleteCommand":    "'set -gx API_KEY fixture-private-value",
		"incompleteFlags":      "set '-gx API_KEY fixture-private-value",
		"quotedQuery":          "'set' '-q' API_KEY fixture-private-value",
		"quotedErase":          "'set' '-gx' '--erase' API_KEY fixture-private-value",
		"plainSet":             "set API_KEY fixture-private-value",
		"globalOnly":           "set -g API_KEY fixture-private-value",
		"exportOnly":           "set -x API_KEY fixture-private-value",
		"query":                "set -q API_KEY fixture-private-value",
		"erase":                "set -gx --erase API_KEY fixture-private-value",
		"unknownFlag":          "set --other -gx API_KEY fixture-private-value",
		"invalidName":          "set -gx 'PASSWORD NAME' fixture-private-value",
		"dashedName":           "set -gx 'API-KEY' fixture-private-value",
		"expandedName":         "set -gx $API_KEY fixture-private-value",
		"incompleteName":       "set -gx 'API_KEY fixture-private-value",
		"noName":               "set --global --export",
		"terminatorOnly":       "set -- API_KEY fixture-private-value",
		"nonsecret":            "set -xg MAX_TOKENS fixture-private-value",
		"prose":                "For example, set -xg API_KEY fixture-private-value",
		"echo":                 "echo set -gx API_KEY fixture-private-value",
		"quotedPrefix":         "'and' export API_KEY=fixture-private-value",
		"exportSet":            "export set -gx API_KEY fixture-private-value",
	} {
		t.Run(name, func(t *testing.T) {
			if err := Text(nil, []byte(line)); err != nil {
				t.Fatalf("Text = %v, want unrelated command allowed", err)
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
		err := Text(nil, data)
		if !bytes.Equal(data, before) {
			t.Fatal("Text mutated input")
		}
		if err != nil && !errors.Is(err, ErrLiteralSecret) {
			t.Fatal("Text returned an unsafe diagnostic")
		}
		if !errors.Is(err, Text(nil, data)) {
			t.Fatal("Text is not deterministic")
		}
	})
}

func TestTextChecksShellAndProseButNotSourceCodeKeywordArguments(t *testing.T) {
	kwargs := []byte("rows = sorted(\n    rows,\n    key=lambda r: r[0],\n)\nf(\n    token=None,\n)\n")
	for name, tc := range map[string]struct {
		identities []string
		withheld   bool
	}{
		"python":         {[]string{"/skills/x/scripts/stylometry.py"}, false},
		"typescript":     {[]string{"/x/hooks/start.ts"}, false},
		"pythonBackup":   {[]string{"/x/tool.py.bak"}, false},
		"shell":          {[]string{"/x/run.sh"}, true},
		"markdown":       {[]string{"/x/SKILL.md"}, true},
		"noExtension":    {[]string{"/x/bin/tool"}, true},
		"unknown":        {nil, true},
		"symlinkToShell": {[]string{"/x/tool.py", "/y/tool.sh"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Text(tc.identities, kwargs); (err != nil) != tc.withheld {
				t.Errorf("Text(%v) = %v, want withheld %v", tc.identities, err, tc.withheld)
			}
		})
	}
}
