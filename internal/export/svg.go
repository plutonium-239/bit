// ABOUTME: SVG generator that converts ANSI-colored text to scalable vector graphics.
// ABOUTME: Merges adjacent same-paint cells into horizontal runs, one path per paint.

package export

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SVGOptions contains configuration for SVG generation
type SVGOptions struct {
	CellWidth  int // ViewBox units per character cell width
	CellHeight int // ViewBox units per character cell height
}

// DefaultSVGOptions returns default SVG generation options (square cells)
func DefaultSVGOptions() SVGOptions {
	return SVGOptions{
		CellWidth:  CellSize,
		CellHeight: CellSize,
	}
}

// TerminalAspectRatioSVGOptions returns SVG options matching terminal character
// aspect ratio (~2:1 height:width), consistent with the PNG exporter.
func TerminalAspectRatioSVGOptions() SVGOptions {
	return SVGOptions{
		CellWidth:  16,
		CellHeight: 32,
	}
}

// svgSegment describes the painted rectangle of a single cell
type svgSegment struct {
	x, y     int // Top-left in viewbox units
	w, h     int // Dimensions in viewbox units
	opacity  float64
	colorHex string
}

// paintKey identifies the fill applied to a segment
type paintKey struct {
	colorHex string
	opacity  float64
}

// GenerateSVG creates an SVG document from rendered ANSI lines.
// Returns the SVG as a string or an error.
func GenerateSVG(lines []string, options SVGOptions) (string, error) {
	if len(lines) == 0 {
		return "", fmt.Errorf("no content to export")
	}

	if options.CellWidth == 0 {
		options.CellWidth = CellSize
	}
	if options.CellHeight == 0 {
		options.CellHeight = CellSize
	}

	// Calculate dimensions based on max line width
	maxWidth := 0
	for _, line := range lines {
		lineWidth := countVisibleChars(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}
	if maxWidth == 0 {
		maxWidth = 1
	}

	imgWidth := maxWidth * options.CellWidth
	imgHeight := len(lines) * options.CellHeight

	// Collect segments grouped by paint
	paints := make(map[paintKey][]string)

	for lineIdx, line := range lines {
		segments := svgLineSegments(line, lineIdx, options)
		runs := mergeSvgRuns(segments)
		for _, run := range runs {
			key := paintKey{colorHex: run.colorHex, opacity: run.opacity}
			paints[key] = append(paints[key], svgRunPath(run))
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\" width=\"%d\" height=\"%d\">\n",
		imgWidth, imgHeight, imgWidth, imgHeight))

	for _, key := range sortedPaintKeys(paints) {
		paths := paints[key]
		sb.WriteString(fmt.Sprintf("  <path fill=\"%s\" fill-opacity=\"%s\" d=\"%s\"/>\n",
			key.colorHex, formatOpacity(key.opacity), strings.Join(paths, "")))
	}

	sb.WriteString("</svg>\n")
	return sb.String(), nil
}

// svgLineSegments walks a line of ANSI text and returns one segment per painted cell
func svgLineSegments(line string, lineIdx int, options SVGOptions) []svgSegment {
	defaultColorHex := "#ffffff"
	currentColor := defaultColorHex
	var segments []svgSegment

	cellY := lineIdx * options.CellHeight
	halfHeight := options.CellHeight / 2

	i := 0
	lineBytes := []byte(line)
	charIdx := 0

	for i < len(lineBytes) {
		if lineBytes[i] == 0x1b && i+1 < len(lineBytes) && lineBytes[i+1] == '[' {
			seqStart := i
			i += 2
			for i < len(lineBytes) && !isAnsiTerminator(lineBytes[i]) {
				i++
			}
			if i < len(lineBytes) {
				i++
			}
			seq := string(lineBytes[seqStart:i])
			if matches := colorRegex.FindStringSubmatch(seq); matches != nil {
				r, _ := strconv.Atoi(matches[1])
				g, _ := strconv.Atoi(matches[2])
				b, _ := strconv.Atoi(matches[3])
				currentColor = fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
			continue
		}

		r, size := utf8.DecodeRune(lineBytes[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}

		cellX := charIdx * options.CellWidth
		switch r {
		case FullBlock:
			segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: options.CellHeight, opacity: 1.0, colorHex: currentColor})
		case UpperHalfBlock:
			segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: halfHeight, opacity: 1.0, colorHex: currentColor})
		case LowerHalfBlock:
			segments = append(segments, svgSegment{x: cellX, y: cellY + halfHeight, w: options.CellWidth, h: halfHeight, opacity: 1.0, colorHex: currentColor})
		case LightShade:
			segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: options.CellHeight, opacity: LightShadeBrightness, colorHex: currentColor})
		case MediumShade:
			segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: options.CellHeight, opacity: MediumShadeBrightness, colorHex: currentColor})
		case DarkShade:
			segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: options.CellHeight, opacity: DarkShadeBrightness, colorHex: currentColor})
		case ' ':
			// Space - leave empty
		default:
			if r > 32 {
				segments = append(segments, svgSegment{x: cellX, y: cellY, w: options.CellWidth, h: options.CellHeight, opacity: 1.0, colorHex: currentColor})
			}
		}

		charIdx++
		i += size
	}

	return segments
}

// mergeSvgRuns merges horizontally adjacent segments with identical geometry and paint
func mergeSvgRuns(segments []svgSegment) []svgSegment {
	var runs []svgSegment

	for _, seg := range segments {
		if n := len(runs); n > 0 {
			prev := &runs[n-1]
			if seg.y == prev.y && seg.h == prev.h &&
				seg.opacity == prev.opacity && seg.colorHex == prev.colorHex &&
				seg.x == prev.x+prev.w {
				prev.w += seg.w
				continue
			}
		}
		runs = append(runs, seg)
	}

	return runs
}

// svgRunPath returns the path data for a run's rectangle, relative to previous subpath
func svgRunPath(run svgSegment) string {
	return fmt.Sprintf("M%d %dh%dv%dh-%dz", run.x, run.y, run.w, run.h, run.w)
}

// sortedPaintKeys returns paint keys in a deterministic order (color, then opacity)
func sortedPaintKeys(paints map[paintKey][]string) []paintKey {
	keys := make([]paintKey, 0, len(paints))
	for k := range paints {
		keys = append(keys, k)
	}
	// Simple insertion sort for deterministic output without importing sort
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			if keys[j].colorHex < keys[j-1].colorHex ||
				(keys[j].colorHex == keys[j-1].colorHex && keys[j].opacity < keys[j-1].opacity) {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			} else {
				break
			}
		}
	}
	return keys
}

// formatOpacity renders an opacity value compactly (e.g. 0.25, 1)
func formatOpacity(opacity float64) string {
	return strconv.FormatFloat(opacity, 'f', -1, 64)
}
