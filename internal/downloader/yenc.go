package downloader

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

// YEncPart holds the decoded result of a yEnc-encoded article part.
type YEncPart struct {
	Name    string
	Size    int
	Line    int
	Part    int
	Total   int
	Begin   int64
	End     int64
	Data    []byte
	CRC32   uint32
	PartCRC uint32
}

// DecodeYEnc decodes a yEnc-encoded byte slice.
func DecodeYEnc(data []byte) (*YEncPart, error) {
	result := &YEncPart{}
	lines := bytes.Split(data, []byte("\n"))
	var dataLines [][]byte
	inData := false
	hasYBeginPart := false

	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")

		if bytes.HasPrefix(line, []byte("=ybegin ")) {
			inData = true
			parseYBeginLine(string(line), result)
			continue
		}
		if bytes.HasPrefix(line, []byte("=ypart ")) {
			hasYBeginPart = true
			parseYPartLine(string(line), result)
			continue
		}
		if bytes.HasPrefix(line, []byte("=yend ")) {
			parseYEndLine(string(line), result)
			inData = false
			continue
		}
		if inData {
			dataLines = append(dataLines, line)
		}
	}

	if result.Name == "" {
		return nil, fmt.Errorf("no =ybegin header found")
	}

	// Decode the data
	decoded := decodeYEncData(dataLines)
	result.Data = decoded

	// Verify CRC if present
	if result.PartCRC != 0 {
		actual := crc32.ChecksumIEEE(decoded)
		if actual != result.PartCRC {
			return nil, fmt.Errorf("part CRC32 mismatch: expected %08x, got %08x", result.PartCRC, actual)
		}
	} else if result.CRC32 != 0 && !hasYBeginPart {
		actual := crc32.ChecksumIEEE(decoded)
		if actual != result.CRC32 {
			return nil, fmt.Errorf("CRC32 mismatch: expected %08x, got %08x", result.CRC32, actual)
		}
	}

	return result, nil
}

func decodeYEncData(lines [][]byte) []byte {
	var result []byte
	for _, line := range lines {
		i := 0
		for i < len(line) {
			b := line[i]
			if b == '=' && i+1 < len(line) {
				i++
				b = line[i] - 64
			}
			result = append(result, b-42)
			i++
		}
	}
	return result
}

func parseYBeginLine(line string, result *YEncPart) {
	parts := parseKeyValues(line[8:]) // skip "=ybegin "
	if v, ok := parts["name"]; ok {
		result.Name = v
	}
	if v, ok := parts["size"]; ok {
		result.Size, _ = strconv.Atoi(v)
	}
	if v, ok := parts["line"]; ok {
		result.Line, _ = strconv.Atoi(v)
	}
	if v, ok := parts["part"]; ok {
		result.Part, _ = strconv.Atoi(v)
	}
	if v, ok := parts["total"]; ok {
		result.Total, _ = strconv.Atoi(v)
	}
}

func parseYPartLine(line string, result *YEncPart) {
	parts := parseKeyValues(line[7:]) // skip "=ypart "
	if v, ok := parts["begin"]; ok {
		result.Begin, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := parts["end"]; ok {
		result.End, _ = strconv.ParseInt(v, 10, 64)
	}
}

func parseYEndLine(line string, result *YEncPart) {
	parts := parseKeyValues(line[6:]) // skip "=yend "
	if v, ok := parts["crc32"]; ok {
		val, _ := strconv.ParseUint(v, 16, 32)
		result.CRC32 = uint32(val)
	}
	if v, ok := parts["pcrc32"]; ok {
		val, _ := strconv.ParseUint(v, 16, 32)
		result.PartCRC = uint32(val)
	}
}

// parseKeyValues parses "key=value key2=value2 name=some file.bin"
// where name is always the last key and can contain spaces.
func parseKeyValues(s string) map[string]string {
	result := make(map[string]string)
	s = strings.TrimSpace(s)

	// Handle "name=" specially since the value can contain spaces
	nameIdx := strings.Index(s, " name=")
	nameVal := ""
	if nameIdx >= 0 {
		nameVal = s[nameIdx+6:]
		s = s[:nameIdx]
		result["name"] = nameVal
	} else if strings.HasPrefix(s, "name=") {
		result["name"] = s[5:]
		return result
	}

	for _, part := range strings.Fields(s) {
		eqIdx := strings.Index(part, "=")
		if eqIdx > 0 {
			key := part[:eqIdx]
			val := part[eqIdx+1:]
			result[key] = val
		}
	}

	return result
}
