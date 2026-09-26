package identity

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parsePlist decodes an XML property list (what diskutil -plist and ioreg -a
// print) into Go values: dict is map[string]any, array is []any, string and
// date are string, integer is int64, real is float64, true/false are bool,
// data is []byte. The standard library has no plist support, and this is all
// the agent needs.
func parsePlist(b []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false // Apple's DOCTYPE and entities are fine to skip
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "plist" {
			for {
				tok, err := d.Token()
				if err != nil {
					return nil, fmt.Errorf("plist: %w", err)
				}
				if se, ok := tok.(xml.StartElement); ok {
					return plistValue(d, se)
				}
				if _, ok := tok.(xml.EndElement); ok {
					return nil, errors.New("plist: empty")
				}
			}
		}
	}
}

func plistValue(d *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		m := map[string]any{}
		key := ""
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					if key, err = plistText(d); err != nil {
						return nil, err
					}
					continue
				}
				v, err := plistValue(d, t)
				if err != nil {
					return nil, err
				}
				m[key] = v
			case xml.EndElement:
				return m, nil
			}
		}
	case "array":
		var a []any
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := plistValue(d, t)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			case xml.EndElement:
				return a, nil
			}
		}
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return se.Name.Local == "true", nil
	}
	text, err := plistText(d)
	if err != nil {
		return nil, err
	}
	switch se.Name.Local {
	case "string", "date":
		return text, nil
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	case "data":
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	}
	return nil, fmt.Errorf("plist: unknown element <%s>", se.Name.Local)
}

// plistText reads character data up to the current element's end.
func plistText(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return "", io.ErrUnexpectedEOF
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		case xml.StartElement:
			return "", fmt.Errorf("plist: unexpected <%s> in text", t.Name.Local)
		}
	}
}
