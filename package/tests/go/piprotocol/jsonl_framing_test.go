package piprotocol_test

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func TestNativeJSONLSplitsOnLFOnly(t *testing.T) {
	stream := "{\"a\":1}\n{\"b\":2}\n"
	var first, second []byte
	advance, token, err := piwire.SplitNativeJSONLRecord([]byte(stream), false)
	if err != nil {
		t.Fatalf("first record rejected: %v", err)
	}
	first = append([]byte(nil), token...)
	rest := []byte(stream)[advance:]
	advance, token, err = piwire.SplitNativeJSONLRecord(rest, false)
	if err != nil {
		t.Fatalf("second record rejected: %v", err)
	}
	second = append([]byte(nil), token...)
	if string(first) != `{"a":1}` || string(second) != `{"b":2}` {
		t.Fatalf("split mismatch: %q %q", first, second)
	}
	if advance != len(rest) {
		t.Fatalf("second advance = %d, want %d", advance, len(rest))
	}
}

func TestNativeJSONLAcceptsCRBeforeLF(t *testing.T) {
	advance, token, err := piwire.SplitNativeJSONLRecord([]byte("{\"a\":1}\r\n"), false)
	if err != nil {
		t.Fatalf("CR-terminated record rejected: %v", err)
	}
	if advance != len("{\"a\":1}\r\n") {
		t.Fatalf("CR advance = %d", advance)
	}
	if string(token) != `{"a":1}` {
		t.Fatalf("CR was not stripped: %q", token)
	}
	record, err := piwire.ParseNativeJSONLRecord(token)
	if err != nil {
		t.Fatalf("CR record did not parse as event: %v", err)
	}
	if !bytes.Contains(record, []byte(`"a"`)) {
		t.Fatalf("CR record payload mismatch: %s", record)
	}
}

func TestNativeJSONLUnicodeSeparatorDoesNotSplit(t *testing.T) {
	// U+2028 inside a JSON string must not delimit a record.
	payload := "{\"text\":\"a b\"}\n"
	advance, token, err := piwire.SplitNativeJSONLRecord([]byte(payload), false)
	if err != nil {
		t.Fatalf("unicode record rejected: %v", err)
	}
	if advance != len(payload) {
		t.Fatalf("unicode separator split the record: advance %d of %d", advance, len(payload))
	}
	if _, err := piwire.ParseNativeJSONLRecord(token); err != nil {
		t.Fatalf("unicode record did not parse as event: %v", err)
	}
}

func TestNativeJSONLRejectsOversizeWithoutAccumulation(t *testing.T) {
	big := make([]byte, piwire.NativeJSONLMaxRecordBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, _, err := piwire.SplitNativeJSONLRecord(big, false); err == nil {
		t.Fatal("oversize line without delimiter was admitted")
	} else if kind, ok := piwire.NativeTransportErrorKindOf(err); !ok || kind != piwire.NativeTransportTooBig {
		t.Fatalf("oversize kind = %v,%v, want too_big", kind, ok)
	}
	framed := append(append([]byte(nil), big...), '\n')
	if _, _, err := piwire.SplitNativeJSONLRecord(framed, false); err == nil {
		t.Fatal("oversize delimited record was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportTooBig {
		t.Fatalf("delimited oversize kind = %v, want too_big", kind)
	}
}

func TestNativeJSONLRejectsInvalidUTF8(t *testing.T) {
	bad := []byte{0xff, 0xfe, '\n'}
	if _, _, err := piwire.SplitNativeJSONLRecord(bad, false); err == nil {
		t.Fatal("invalid UTF-8 record was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportInvalidUTF8 {
		t.Fatalf("utf8 kind = %v, want invalid_utf8", kind)
	}
}

func TestNativeJSONLIncompleteTrailingStaysIncomplete(t *testing.T) {
	if _, _, err := piwire.SplitNativeJSONLRecord([]byte(`{"a":1}`), true); err == nil {
		t.Fatal("incomplete trailing record was admitted as an event")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportIncomplete {
		t.Fatalf("incomplete kind = %v, want incomplete", kind)
	}
}

func TestNativeJSONLLogsAreNotEvents(t *testing.T) {
	for _, line := range []string{"not json", "[1,2,3]", "", "null"} {
		if _, err := piwire.ParseNativeJSONLRecord([]byte(line)); err == nil {
			t.Fatalf("log line %q was admitted as an event", line)
		} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportLogLine && kind != piwire.NativeTransportInvalidJSON {
			t.Fatalf("log line %q kind = %v, want log_line", line, kind)
		}
	}
	event, err := piwire.ParseNativeJSONLRecord([]byte(`{"method":"agent_settled","params":{}}`))
	if err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if !strings.Contains(string(event), "agent_settled") {
		t.Fatalf("event payload mismatch: %s", event)
	}
}

func TestNativeJSONLEncodeBoundsWithoutTruncation(t *testing.T) {
	raw, err := piwire.EncodeNativeJSONLRecord(map[string]any{"method": "prompt", "id": 1})
	if err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("encoded record misses LF delimiter: %q", raw)
	}
	huge := strings.Repeat("a", piwire.NativeJSONLMaxWriteBytes+1)
	if _, err := piwire.EncodeNativeJSONLRecord(map[string]any{"text": huge}); err == nil {
		t.Fatal("oversize write was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportTooBig {
		t.Fatalf("encode oversize kind = %v, want too_big", kind)
	}
	_ = bufio.NewReader(bytes.NewReader(raw))
}
