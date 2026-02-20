package downloader

import (
	"fmt"
	"hash/crc32"
	"testing"
)

func TestDecodeYEncSinglePart(t *testing.T) {
	// Simple yEnc-encoded test: "Hello" encoded with yEnc (each byte + 42)
	// H(72) -> 114='r', e(101) -> 143 -> escaped: =\x8f... actually let's do it properly
	// yEnc: byte + 42, mod 256. If result is 0x00, 0x0A, 0x0D, 0x3D (NUL, LF, CR, =), escape it.
	// 'H'=72 -> 72+42=114='r'
	// 'e'=101 -> 101+42=143 (not special) = byte 143
	// 'l'=108 -> 108+42=150 = byte 150
	// 'l'=108 -> 150
	// 'o'=111 -> 111+42=153 = byte 153

	input := "Hello World!"
	encoded := yencEncode([]byte(input))
	crc := crc32.ChecksumIEEE([]byte(input))

	data := []byte("=ybegin line=128 size=12 name=test.txt\r\n")
	data = append(data, encoded...)
	data = append(data, []byte("\r\n=yend size=12 crc32="+crc32hex(crc)+"\r\n")...)

	result, err := DecodeYEnc(data)
	if err != nil {
		t.Fatalf("DecodeYEnc failed: %v", err)
	}

	if result.Name != "test.txt" {
		t.Errorf("expected name 'test.txt', got %q", result.Name)
	}
	if result.Size != 12 {
		t.Errorf("expected size 12, got %d", result.Size)
	}
	if string(result.Data) != input {
		t.Errorf("expected data %q, got %q", input, string(result.Data))
	}
}

func TestDecodeYEncMultiPart(t *testing.T) {
	input := "Part1Data"
	encoded := yencEncode([]byte(input))
	crc := crc32.ChecksumIEEE([]byte(input))

	data := []byte("=ybegin part=1 total=3 line=128 size=100 name=bigfile.bin\r\n")
	data = append(data, []byte("=ypart begin=1 end=9\r\n")...)
	data = append(data, encoded...)
	data = append(data, []byte("\r\n=yend size=9 pcrc32="+crc32hex(crc)+"\r\n")...)

	result, err := DecodeYEnc(data)
	if err != nil {
		t.Fatalf("DecodeYEnc failed: %v", err)
	}

	if result.Part != 1 {
		t.Errorf("expected part 1, got %d", result.Part)
	}
	if result.Total != 3 {
		t.Errorf("expected total 3, got %d", result.Total)
	}
	if result.Begin != 1 {
		t.Errorf("expected begin 1, got %d", result.Begin)
	}
	if result.End != 9 {
		t.Errorf("expected end 9, got %d", result.End)
	}
	if string(result.Data) != input {
		t.Errorf("expected data %q, got %q", input, string(result.Data))
	}
}

func TestDecodeYEncCRCMismatch(t *testing.T) {
	input := "test data"
	encoded := yencEncode([]byte(input))

	data := []byte("=ybegin line=128 size=9 name=test.bin\r\n")
	data = append(data, encoded...)
	data = append(data, []byte("\r\n=yend size=9 crc32=deadbeef\r\n")...)

	_, err := DecodeYEnc(data)
	if err == nil {
		t.Fatal("expected CRC mismatch error")
	}
}

func TestDecodeYEncEscapeSequences(t *testing.T) {
	// Test bytes that need escaping: 0x00, 0x0A, 0x0D, 0x3D
	// 0x00: original byte = 256-42 = 214
	// 0x0A: original byte = 10-42 = -32 -> 224
	// 0x0D: original byte = 13-42 = -29 -> 227
	// 0x3D: original byte = 61-42 = 19
	input := []byte{214, 224, 227, 19}
	encoded := yencEncode(input)
	crc := crc32.ChecksumIEEE(input)

	data := []byte("=ybegin line=128 size=4 name=esc.bin\r\n")
	data = append(data, encoded...)
	data = append(data, []byte("\r\n=yend size=4 crc32="+crc32hex(crc)+"\r\n")...)

	result, err := DecodeYEnc(data)
	if err != nil {
		t.Fatalf("DecodeYEnc failed: %v", err)
	}

	for i, b := range result.Data {
		if b != input[i] {
			t.Errorf("byte %d: expected %d, got %d", i, input[i], b)
		}
	}
}

func TestDecodeYEncNoHeader(t *testing.T) {
	_, err := DecodeYEnc([]byte("just some random data\r\n"))
	if err == nil {
		t.Fatal("expected error for missing ybegin header")
	}
}

func TestParseKeyValues(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]string
	}{
		{
			"line=128 size=100 name=test.txt",
			map[string]string{"line": "128", "size": "100", "name": "test.txt"},
		},
		{
			"line=128 size=100 name=file with spaces.txt",
			map[string]string{"line": "128", "size": "100", "name": "file with spaces.txt"},
		},
	}

	for _, tt := range tests {
		result := parseKeyValues(tt.input)
		for k, v := range tt.expected {
			if result[k] != v {
				t.Errorf("parseKeyValues(%q)[%q] = %q, want %q", tt.input, k, result[k], v)
			}
		}
	}
}

// Helper: yEnc encode a byte slice (for test data generation)
func yencEncode(data []byte) []byte {
	var result []byte
	for _, b := range data {
		encoded := byte((int(b) + 42) % 256)
		switch encoded {
		case 0x00, 0x0A, 0x0D, 0x3D: // NUL, LF, CR, =
			result = append(result, '=')
			result = append(result, encoded+64)
		default:
			result = append(result, encoded)
		}
	}
	return result
}

func crc32hex(crc uint32) string {
	return fmt.Sprintf("%08x", crc)
}
