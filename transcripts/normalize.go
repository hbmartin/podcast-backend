package transcripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

type NormalizedArtifact struct {
	Body      []byte
	MediaType string
	Format    string
}

var srtTiming = regexp.MustCompile(`(?m)^(\d{1,2}:\d{2}:\d{2}),(\d{3})\s+-->\s+(\d{1,2}:\d{2}:\d{2}),(\d{3})`)

func NormalizePublisherContent(content []byte, contentType string) (NormalizedArtifact, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "text/vtt":
		if !utf8.Valid(content) || !strings.HasPrefix(strings.TrimSpace(string(content)), "WEBVTT") {
			return NormalizedArtifact{}, fmt.Errorf("invalid WebVTT")
		}
		return NormalizedArtifact{Body: content, MediaType: "text/vtt", Format: "vtt"}, nil
	case "application/srt", "application/x-subrip", "text/srt":
		if !utf8.Valid(content) {
			return NormalizedArtifact{}, fmt.Errorf("invalid SRT")
		}
		converted := srtTiming.ReplaceAllString(string(content), "$1.$2 --> $3.$4")
		return NormalizedArtifact{Body: []byte("WEBVTT\n\n" + converted), MediaType: "text/vtt", Format: "vtt"}, nil
	case "application/json", "text/json":
		vtt, err := podcastJSONToVTT(content)
		if err != nil {
			return NormalizedArtifact{}, err
		}
		return NormalizedArtifact{Body: vtt, MediaType: "text/vtt", Format: "vtt"}, nil
	case "text/html":
		sanitized, err := sanitizeTranscriptHTML(content)
		if err != nil {
			return NormalizedArtifact{}, err
		}
		return NormalizedArtifact{Body: sanitized, MediaType: "text/html; charset=utf-8", Format: "html"}, nil
	case "text/plain":
		if !utf8.Valid(content) {
			return NormalizedArtifact{}, fmt.Errorf("invalid UTF-8 text")
		}
		return NormalizedArtifact{Body: content, MediaType: "text/plain; charset=utf-8", Format: "plain"}, nil
	default:
		return NormalizedArtifact{}, fmt.Errorf("unsupported transcript type %q", mediaType)
	}
}

type timedSegment struct {
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
	Body      string  `json:"body"`
	Text      string  `json:"text"`
	Speaker   string  `json:"speaker"`
}

func podcastJSONToVTT(content []byte) ([]byte, error) {
	var envelope struct {
		Segments []timedSegment `json:"segments"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil || len(envelope.Segments) == 0 {
		return nil, fmt.Errorf("invalid Podcast JSON transcript")
	}
	sort.SliceStable(envelope.Segments, func(i, j int) bool { return envelope.Segments[i].StartTime < envelope.Segments[j].StartTime })
	var output strings.Builder
	output.WriteString("WEBVTT\n\n")
	for index, segment := range envelope.Segments {
		text := strings.TrimSpace(segment.Body)
		if text == "" {
			text = strings.TrimSpace(segment.Text)
		}
		if text == "" || segment.StartTime < 0 || segment.EndTime <= segment.StartTime {
			return nil, fmt.Errorf("invalid timed segment")
		}
		fmt.Fprintf(&output, "%d\n%s --> %s\n", index+1, vttTimestamp(segment.StartTime), vttTimestamp(segment.EndTime))
		if segment.Speaker != "" {
			output.WriteString("<v " + strings.ReplaceAll(segment.Speaker, ">", "") + ">")
		}
		output.WriteString(html.EscapeString(text))
		output.WriteString("\n\n")
	}
	return []byte(output.String()), nil
}

func vttTimestamp(seconds float64) string {
	milliseconds := int64(seconds*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", milliseconds/3600000, (milliseconds/60000)%60, (milliseconds/1000)%60, milliseconds%1000)
}

func sanitizeTranscriptHTML(content []byte) ([]byte, error) {
	document, err := xhtml.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	var clean func(*xhtml.Node)
	clean = func(node *xhtml.Node) {
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == xhtml.ElementNode && (child.Data == "script" || child.Data == "style" || child.Data == "iframe" || child.Data == "object" || child.Data == "embed") {
				node.RemoveChild(child)
			} else {
				if child.Type == xhtml.ElementNode {
					attributes := child.Attr[:0]
					for _, attribute := range child.Attr {
						key := strings.ToLower(attribute.Key)
						if strings.HasPrefix(key, "on") || key == "style" {
							continue
						}
						if (key == "href" || key == "src") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(attribute.Val)), "javascript:") {
							continue
						}
						attributes = append(attributes, attribute)
					}
					child.Attr = attributes
				}
				clean(child)
			}
			child = next
		}
	}
	clean(document)
	var output bytes.Buffer
	for child := document.FirstChild; child != nil; child = child.NextSibling {
		if err := xhtml.Render(&output, child); err != nil {
			return nil, err
		}
	}
	if output.Len() == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return output.Bytes(), nil
}

func parseJSONNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	}
	return 0
}
