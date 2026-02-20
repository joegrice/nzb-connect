package nzb

import (
	"strings"
	"testing"
)

const testNZB = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="user@example.com" date="1234567890" subject="Test File [1/2] - &quot;testfile.rar&quot; yEnc (1/5)">
    <groups>
      <group>alt.binaries.test</group>
      <group>alt.binaries.misc</group>
    </groups>
    <segments>
      <segment bytes="384000" number="1">msg-id-001@news.example.com</segment>
      <segment bytes="384000" number="2">msg-id-002@news.example.com</segment>
      <segment bytes="384000" number="3">msg-id-003@news.example.com</segment>
      <segment bytes="384000" number="4">msg-id-004@news.example.com</segment>
      <segment bytes="128000" number="5">msg-id-005@news.example.com</segment>
    </segments>
  </file>
  <file poster="user@example.com" date="1234567890" subject="Test File [2/2] - &quot;testfile.r00&quot; yEnc (1/3)">
    <groups>
      <group>alt.binaries.test</group>
    </groups>
    <segments>
      <segment bytes="384000" number="1">msg-id-006@news.example.com</segment>
      <segment bytes="384000" number="2">msg-id-007@news.example.com</segment>
      <segment bytes="128000" number="3">msg-id-008@news.example.com</segment>
    </segments>
  </file>
</nzb>`

func TestParse(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(nzb.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(nzb.Files))
	}
}

func TestFileProperties(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	f := nzb.Files[0]
	if f.Poster != "user@example.com" {
		t.Errorf("expected poster 'user@example.com', got %q", f.Poster)
	}
	if len(f.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(f.Groups))
	}
	if f.Groups[0] != "alt.binaries.test" {
		t.Errorf("expected group 'alt.binaries.test', got %q", f.Groups[0])
	}
	if len(f.Segments) != 5 {
		t.Errorf("expected 5 segments, got %d", len(f.Segments))
	}
}

func TestSegmentProperties(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	seg := nzb.Files[0].Segments[0]
	if seg.Bytes != 384000 {
		t.Errorf("expected bytes 384000, got %d", seg.Bytes)
	}
	if seg.Number != 1 {
		t.Errorf("expected number 1, got %d", seg.Number)
	}
	if seg.MessageID != "msg-id-001@news.example.com" {
		t.Errorf("expected message ID 'msg-id-001@news.example.com', got %q", seg.MessageID)
	}
}

func TestFilename(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	name := nzb.Files[0].Filename()
	if name != "testfile.rar" {
		t.Errorf("expected filename 'testfile.rar', got %q", name)
	}

	name2 := nzb.Files[1].Filename()
	if name2 != "testfile.r00" {
		t.Errorf("expected filename 'testfile.r00', got %q", name2)
	}
}

func TestTotalSize(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// File 1: 4*384000 + 128000 = 1664000
	size1 := nzb.Files[0].TotalSize()
	if size1 != 1664000 {
		t.Errorf("expected file 1 size 1664000, got %d", size1)
	}

	// File 2: 2*384000 + 128000 = 896000
	size2 := nzb.Files[1].TotalSize()
	if size2 != 896000 {
		t.Errorf("expected file 2 size 896000, got %d", size2)
	}

	// Total: 2560000
	total := nzb.TotalSize()
	if total != 2560000 {
		t.Errorf("expected total size 2560000, got %d", total)
	}
}

func TestTotalSegments(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if nzb.TotalSegments() != 8 {
		t.Errorf("expected 8 total segments, got %d", nzb.TotalSegments())
	}
}

func TestSortedSegments(t *testing.T) {
	nzb, err := Parse(strings.NewReader(testNZB))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	sorted := nzb.Files[0].SortedSegments()
	for i := 0; i < len(sorted)-1; i++ {
		if sorted[i].Number >= sorted[i+1].Number {
			t.Errorf("segments not sorted: %d >= %d", sorted[i].Number, sorted[i+1].Number)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	_, err := Parse(strings.NewReader(`<?xml version="1.0"?><nzb></nzb>`))
	if err == nil {
		t.Fatal("expected error for empty NZB")
	}
}

func TestParseInvalidXML(t *testing.T) {
	_, err := Parse(strings.NewReader("not xml at all"))
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
	}
	for _, tt := range tests {
		result := FormatSize(tt.bytes)
		if result != tt.expected {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
		}
	}
}
