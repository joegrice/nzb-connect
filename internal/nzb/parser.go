package nzb

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Meta represents a <meta> element in the NZB header.
type Meta struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// NZB represents a parsed NZB file.
type NZB struct {
	XMLName xml.Name `xml:"nzb"`
	Head    []Meta   `xml:"head>meta"`
	Files   []File   `xml:"file"`
}

// Password returns the password from the NZB metadata, or "" if none.
func (n *NZB) Password() string {
	for _, m := range n.Head {
		if strings.EqualFold(m.Type, "password") {
			return strings.TrimSpace(m.Value)
		}
	}
	return ""
}

// File represents a file within an NZB.
type File struct {
	Poster   string    `xml:"poster,attr"`
	Date     string    `xml:"date,attr"`
	Subject  string    `xml:"subject,attr"`
	Groups   []string  `xml:"groups>group"`
	Segments []Segment `xml:"segments>segment"`
}

// Segment represents a single segment (article) of a file.
type Segment struct {
	Bytes     int    `xml:"bytes,attr"`
	Number    int    `xml:"number,attr"`
	MessageID string `xml:",chardata"`
}

// TotalSize returns the total size of all segments in the file in bytes.
func (f *File) TotalSize() int64 {
	var total int64
	for _, seg := range f.Segments {
		total += int64(seg.Bytes)
	}
	return total
}

// Filename extracts the filename from the subject line.
// Subject lines typically look like: "description [01/10] - \"filename.ext\" yEnc (1/5)"
func (f *File) Filename() string {
	subj := f.Subject

	// Try to extract filename from quotes
	start := strings.Index(subj, "\"")
	if start >= 0 {
		end := strings.Index(subj[start+1:], "\"")
		if end >= 0 {
			return subj[start+1 : start+1+end]
		}
	}

	// Fallback: try to parse from subject
	parts := strings.Fields(subj)
	for _, p := range parts {
		if strings.Contains(p, ".") && !strings.HasPrefix(p, "(") {
			return strings.Trim(p, "\"'[]")
		}
	}

	return subj
}

// SortedSegments returns segments sorted by number.
func (f *File) SortedSegments() []Segment {
	sorted := make([]Segment, len(f.Segments))
	copy(sorted, f.Segments)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Number < sorted[j].Number
	})
	return sorted
}

// TotalSize returns the total size of all files in the NZB.
func (n *NZB) TotalSize() int64 {
	var total int64
	for _, f := range n.Files {
		total += f.TotalSize()
	}
	return total
}

// TotalSegments returns the total number of segments across all files.
func (n *NZB) TotalSegments() int {
	total := 0
	for _, f := range n.Files {
		total += len(f.Segments)
	}
	return total
}

// Parse parses an NZB file from a reader.
func Parse(r io.Reader) (*NZB, error) {
	var nzb NZB
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&nzb); err != nil {
		return nil, fmt.Errorf("decoding NZB XML: %w", err)
	}

	if len(nzb.Files) == 0 {
		return nil, fmt.Errorf("NZB contains no files")
	}

	// Validate segments
	for i, f := range nzb.Files {
		if len(f.Segments) == 0 {
			return nil, fmt.Errorf("file %d (%s) has no segments", i, f.Subject)
		}
		for j, seg := range f.Segments {
			if seg.MessageID == "" {
				return nil, fmt.Errorf("file %d segment %d has empty message ID", i, j)
			}
		}
	}

	return &nzb, nil
}

// ParseFile parses an NZB file from a file path.
func ParseFile(path string) (*NZB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening NZB file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// ParseBytes parses an NZB file from a byte slice.
func ParseBytes(data []byte) (*NZB, error) {
	return Parse(strings.NewReader(string(data)))
}

// FormatSize formats a byte count as a human-readable string.
func FormatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return strconv.FormatFloat(float64(bytes)/float64(GB), 'f', 2, 64) + " GB"
	case bytes >= MB:
		return strconv.FormatFloat(float64(bytes)/float64(MB), 'f', 2, 64) + " MB"
	case bytes >= KB:
		return strconv.FormatFloat(float64(bytes)/float64(KB), 'f', 2, 64) + " KB"
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}
