// Browser request IDs and host transport IDs are separate wire domains.
// Browser string "001" never becomes host integer 1. The SDK-based assistant
// has no native child-process correlation domain.
package piprotocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const HostV2MaxSafeInteger = 9007199254740991
const HostV2BrowserIDMaxBytes = 256

type BrowserRequestID string
type HostRequestID int64

func (id BrowserRequestID) Validate() error {
	raw := string(id)
	if len(raw) == 0 || len(raw) > HostV2BrowserIDMaxBytes {
		return fmt.Errorf("browser request id out of bounds")
	}
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return fmt.Errorf("browser request id is invalid")
	}
	return nil
}

func (id HostRequestID) Validate() error {
	if id <= 0 || int64(id) > HostV2MaxSafeInteger {
		return fmt.Errorf("host request id out of bounds")
	}
	return nil
}

func (id BrowserRequestID) MarshalJSON() ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(id))
}

func (id *BrowserRequestID) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("browser request id must be a string")
	}
	candidate := BrowserRequestID(value)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*id = candidate
	return nil
}

func (id HostRequestID) MarshalJSON() ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(int64(id))
}

func (id *HostRequestID) UnmarshalJSON(raw []byte) error {
	parsed, err := parseHostV2NumberID(raw, HostV2MaxSafeInteger)
	if err != nil {
		return err
	}
	*id = HostRequestID(parsed)
	return nil
}

func parseHostV2NumberID(raw []byte, max int64) (int64, error) {
	var value int64
	// Integer decoding rejects quoted strings, fractions and exponent spellings.
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("host request id must use integer spelling")
	}
	if value <= 0 || value > max {
		return 0, fmt.Errorf("host request id out of bounds")
	}
	return value, nil
}

func HostV2ParseBrowserID(raw json.RawMessage) (BrowserRequestID, error) {
	var id BrowserRequestID
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", err
	}
	return id, nil
}

func HostV2ParseHostID(raw json.RawMessage) (HostRequestID, error) {
	var id HostRequestID
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, err
	}
	return id, nil
}
