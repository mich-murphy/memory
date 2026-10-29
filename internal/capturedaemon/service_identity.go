package capturedaemon

import (
	"encoding/xml"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const serviceDescription = "Experimental local session capture (not production cutover)"

// Installed identity is stable across executable/config relocation and template
// tuning. It is not equality with the next desired plan. File ownership, mode,
// bounds and path trust are checked separately before parsing.
func managedService(path string, body []byte) bool {
	var args []string
	var err error
	if strings.HasSuffix(path, ".plist") {
		args, err = plistServiceArgs(body)
	} else {
		args, err = systemdServiceArgs(body)
	}
	return err == nil && len(args) == 5 && args[1] == "capture-v1" && args[2] == "run" && args[3] == "--config" &&
		SafePath(args[0]) == nil && SafePath(args[4]) == nil && filepath.Base(path) == ServiceName+filepath.Ext(path)
}

type plistElement struct {
	XMLName  xml.Name
	Text     string         `xml:",chardata"`
	Children []plistElement `xml:",any"`
}

func plistServiceArgs(body []byte) ([]string, error) {
	var doc struct {
		XMLName xml.Name     `xml:"plist"`
		Dict    plistElement `xml:"dict"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	label := ""
	var args []string
	seen := map[string]bool{}
	values := doc.Dict.Children
	if len(values)%2 != 0 {
		return nil, errors.New("invalid service identity")
	}
	for n := 0; n < len(values); n += 2 {
		key, value := values[n], values[n+1]
		if key.XMLName.Local != "key" || seen[key.Text] {
			return nil, errors.New("invalid service identity")
		}
		seen[key.Text] = true
		switch key.Text {
		case "Label":
			if value.XMLName.Local != "string" {
				return nil, errors.New("invalid service identity")
			}
			label = value.Text
		case "ProgramArguments":
			if value.XMLName.Local != "array" {
				return nil, errors.New("invalid service identity")
			}
			var err error
			args, err = plistArguments(value)
			if err != nil {
				return nil, err
			}
		}
	}
	if label != ServiceName {
		return nil, errors.New("foreign service identity")
	}
	return args, nil
}

func plistArguments(value plistElement) ([]string, error) {
	args := make([]string, 0, len(value.Children))
	for _, arg := range value.Children {
		if arg.XMLName.Local != "string" {
			return nil, errors.New("invalid service identity")
		}
		args = append(args, arg.Text)
	}
	return args, nil
}

var quotedServiceWord = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

func systemdCommand(body []byte) (string, error) {
	section, description, command := "", "", ""
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section == "[Unit]" && strings.HasPrefix(line, "Description=") {
			if description != "" {
				return "", errors.New("duplicate service identity")
			}
			description = strings.TrimPrefix(line, "Description=")
		}
		if section == "[Service]" && strings.HasPrefix(line, "ExecStart=") {
			if command != "" {
				return "", errors.New("duplicate service command")
			}
			command = strings.TrimPrefix(line, "ExecStart=")
		}
	}
	if description != serviceDescription {
		return "", errors.New("foreign service identity")
	}
	return command, nil
}

func systemdServiceArgs(body []byte) ([]string, error) {
	command, err := systemdCommand(body)
	if err != nil {
		return nil, err
	}
	words := quotedServiceWord.FindAllString(command, -1)
	if len(words) != 5 || strings.Join(words, " ") != command {
		return nil, errors.New("invalid service command")
	}
	args := make([]string, len(words))
	for n, word := range words {
		value, err := strconv.Unquote(word)
		if err != nil {
			return nil, err
		}
		value = strings.ReplaceAll(strings.ReplaceAll(value, "%%", "%"), "$$", "$")
		if systemdQuote(value) != word {
			return nil, errors.New("invalid service quoting")
		}
		args[n] = value
	}
	return args, nil
}
