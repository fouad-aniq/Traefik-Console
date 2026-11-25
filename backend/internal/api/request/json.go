package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/traefik-console/internal/api/response"
)

const defaultMaxBodyBytes int64 = 1024 * 1024
const maxJSONDepth = 32

type NullPolicy uint8

const (
	RejectNulls NullPolicy = iota
	SpecNulls
)

type Options struct {
	MaxBytes   int64
	NullPolicy NullPolicy
}

var errNullProperty = errors.New("null property outside opaque spec")

// DecodeObject keeps content-type and case-insensitive struct binding compatibility.
// Nulls are allowed only below the middleware spec object when explicitly requested.
func DecodeObject(w http.ResponseWriter, r *http.Request, dst any, options Options) bool {
	limit := options.MaxBytes
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.WriteCustomError(w, 413, "PAYLOAD_TOO_LARGE", "request.exceeds_limit", nil, false)
		} else {
			response.WriteCustomError(w, 400, "BAD_REQUEST", "request.body_unreadable", nil, true)
		}
		return false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		response.WriteCustomError(w, 400, "BAD_REQUEST", "request.expected_object", nil, false)
		return false
	}
	if !utf8.Valid(raw) {
		response.WriteCustomError(w, 400, "BAD_REQUEST", "request.invalid_structure", nil, false)
		return false
	}
	if err = inspectJSON(raw, options.NullPolicy); err != nil {
		message := "request.invalid_structure"
		if errors.Is(err, errNullProperty) {
			message = "request.null_properties"
		}
		response.WriteCustomError(w, 400, "BAD_REQUEST", message, nil, false)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(dst); err != nil {
		response.WriteCustomError(w, 400, "BAD_REQUEST", "request.invalid_object", nil, false)
		return false
	}
	return true
}

// One bounded token walk checks duplicate keys, depth, trailing data and null scope.
func inspectJSON(raw []byte, policy NullPolicy) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func(int, bool) error
	walk = func(depth int, inSpec bool) error {
		if depth > maxJSONDepth {
			return fmt.Errorf("JSON depth exceeded")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case nil:
			if policy != SpecNulls || !inSpec || depth <= 1 {
				return errNullProperty
			}
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate object key")
				}
				seen[name] = true
				childSpec := inSpec || depth == 0 && strings.EqualFold(name, "spec")
				if err = walk(depth+1, childSpec); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case json.Delim('['):
			for decoder.More() {
				if err = walk(depth+1, inSpec); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := walk(0, false); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
