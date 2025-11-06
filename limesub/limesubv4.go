package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"github.com/sqweek/dialog"
)

// ---------- Untuk resample ASS ----------
// ---------- Konfigurasi target ----------
const (
	targetPlayResX  = 1920.0
	targetPlayResY  = 1080.0
	targetFontName  = "Basic Comical NC"
	resStyleLine    = "Style: res,Basic Comical NC,1080,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,0,0,0,0,1,2,2,2,10,10,10,1"
	defaultPlayResX = 1280.0
	defaultPlayResY = 720.0
)

// ---------- Utility helpers ----------
func parseFloatSafe(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

// splitNPreserveTrailing: split string by sep into at most n parts (like strings.SplitN),
// but when n > 0 and there are fewer separators, it still returns len<=n parts.
// (we will use to split Style fields into exactly len(formatFields) parts by doing SplitN with count)
func splitNPreserveTrailing(s string, sep rune, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	parts := make([]string, 0, n)
	cur := bytes.NewBuffer(nil)
	count := 1
	for _, ch := range s {
		if ch == sep && count < n {
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
			count++
			continue
		}
		cur.WriteRune(ch)
	}
	parts = append(parts, strings.TrimSpace(cur.String()))
	return parts
}

// scaleFloat formats scaled value: integer without decimals, otherwise 2 decimals trimmed trailing zeros.
func scaleFloatFormat(v float64) string {
	// if v is close to int:
	if float64(int64(v)) == v {
		return fmt.Sprintf("%d", int64(v))
	}
	// else 2 decimals but trim trailing zeroes
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// scaleXYList: scale alternating numbers in a string (used for vector paths)
func scaleXYList(s string, ratioX, ratioY float64) string {
	re := regexp.MustCompile(`-?\d+(\.\d+)?`)
	indices := re.FindAllStringIndex(s, -1)
	if len(indices) == 0 {
		return s
	}
	out := bytes.NewBuffer(nil)
	last := 0
	count := 0
	for _, idx := range indices {
		out.WriteString(s[last:idx[0]])
		num := s[idx[0]:idx[1]]
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			out.WriteString(num)
		} else {
			if count%2 == 0 {
				out.WriteString(scaleFloatFormat(f * ratioX))
			} else {
				out.WriteString(scaleFloatFormat(f * ratioY))
			}
		}
		last = idx[1]
		count++
	}
	out.WriteString(s[last:])
	return out.String()
}

// scaleNumberInString: replace a number string with scaled value
func scaleNumberString(numStr string, scale float64) string {
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return numStr
	}
	return scaleFloatFormat(f * scale)
}

// ---------- Tag scaling (best-effort) ----------
func scaleTags(content string, ratioX, ratioY float64) string {
	s := content

	// \fs and \fsp -> scale by ratioY
	reFs := regexp.MustCompile(`\\fs(-?\d+(\.\d+)?)`)
	s = reFs.ReplaceAllStringFunc(s, func(m string) string {
		sub := reFs.FindStringSubmatch(m)
		return `\fs` + scaleNumberString(sub[1], ratioY)
	})
	reFsp := regexp.MustCompile(`\\fsp(-?\d+(\.\d+)?)`)
	s = reFsp.ReplaceAllStringFunc(s, func(m string) string {
		sub := reFsp.FindStringSubmatch(m)
		return `\fsp` + scaleNumberString(sub[1], ratioY)
	})

	// \pos(x,y)
	rePos := regexp.MustCompile(`\\pos\(\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*\)`)
	s = rePos.ReplaceAllStringFunc(s, func(m string) string {
		sub := rePos.FindStringSubmatch(m)
		x := scaleNumberString(sub[1], ratioX)
		y := scaleNumberString(sub[3], ratioY)
		return `\pos(` + x + "," + y + `)`
	})

	// \org(x,y)
	reOrg := regexp.MustCompile(`\\org\(\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*\)`)
	s = reOrg.ReplaceAllStringFunc(s, func(m string) string {
		sub := reOrg.FindStringSubmatch(m)
		x := scaleNumberString(sub[1], ratioX)
		y := scaleNumberString(sub[3], ratioY)
		return `\org(` + x + "," + y + `)`
	})

	// \move(x1,y1,x2,y2[,t1,t2])
	reMove := regexp.MustCompile(`\\move\(\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)([^)]*)\)`)
	s = reMove.ReplaceAllStringFunc(s, func(m string) string {
		sub := reMove.FindStringSubmatch(m)
		x1 := scaleNumberString(sub[1], ratioX)
		y1 := scaleNumberString(sub[3], ratioY)
		x2 := scaleNumberString(sub[5], ratioX)
		y2 := scaleNumberString(sub[7], ratioY)
		tail := sub[8]
		return `\move(` + x1 + "," + y1 + "," + x2 + "," + y2 + tail + `)`
	})

	// \clip(...) and \iclip(...)
	reClip := regexp.MustCompile(`\\(i?clip)\(\s*([^\)]*)\s*\)`)
	s = reClip.ReplaceAllStringFunc(s, func(m string) string {
		sub := reClip.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		fn := sub[1]
		content := strings.TrimSpace(sub[2])
		// vector path starts with letters like "m" or "M" or contains letters - scale numbers inside alternately
		if len(content) > 0 && regexp.MustCompile(`^[a-zA-Z]`).MatchString(content) {
			return `\` + fn + `(` + scaleXYList(content, ratioX, ratioY) + `)`
		}
		// otherwise treat as numbers separated by comma
		nums := regexp.MustCompile(`-?\d+(\.\d+)?`).FindAllString(content, -1)
		if len(nums) >= 4 {
			out := content
			// replace first four occurrences with scaled ones
			out = regexp.MustCompile(regexp.QuoteMeta(nums[0])).ReplaceAllString(out, scaleNumberString(nums[0], ratioX))
			out = regexp.MustCompile(regexp.QuoteMeta(nums[1])).ReplaceAllString(out, scaleNumberString(nums[1], ratioY))
			out = regexp.MustCompile(regexp.QuoteMeta(nums[2])).ReplaceAllString(out, scaleNumberString(nums[2], ratioX))
			out = regexp.MustCompile(regexp.QuoteMeta(nums[3])).ReplaceAllString(out, scaleNumberString(nums[3], ratioY))
			return `\` + fn + `(` + out + `)`
		}
		// fallback: scale alternately
		return `\` + fn + `(` + scaleXYList(content, ratioX, ratioY) + `)`
	})

	// pixel-like props -> vertical scale
	rePixel := regexp.MustCompile(`\\(bord|shad|be|blur)(-?\d+(\.\d+)?)`)
	s = rePixel.ReplaceAllStringFunc(s, func(m string) string {
		sub := rePixel.FindStringSubmatch(m)
		return `\` + sub[1] + scaleNumberString(sub[2], ratioY)
	})

	// margins: \margins(l,r,t,b)
	reMargins := regexp.MustCompile(`\\margins\(\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*,\s*(-?\d+(\.\d+)?)\s*\)`)
	s = reMargins.ReplaceAllStringFunc(s, func(m string) string {
		sub := reMargins.FindStringSubmatch(m)
		l := scaleNumberString(sub[1], ratioX)
		r := scaleNumberString(sub[3], ratioX)
		t := scaleNumberString(sub[5], ratioY)
		b := scaleNumberString(sub[7], ratioY)
		return `\margins(` + l + "," + r + "," + t + "," + b + `)`
	})
	// single margins
	reMarginSingle := regexp.MustCompile(`\\margin([lrvbt])(-?\d+(\.\d+)?)`)
	s = reMarginSingle.ReplaceAllStringFunc(s, func(m string) string {
		sub := reMarginSingle.FindStringSubmatch(m)
		side := sub[1]
		val := sub[2]
		switch side {
		case "l", "r":
			return `\margin` + side + scaleNumberString(val, ratioX)
		default:
			return `\margin` + side + scaleNumberString(val, ratioY)
		}
	})

	// \fax \fay
	reFax := regexp.MustCompile(`\\fax(-?\d+(\.\d+)?)`)
	s = reFax.ReplaceAllStringFunc(s, func(m string) string {
		sub := reFax.FindStringSubmatch(m)
		return `\fax` + scaleNumberString(sub[1], ratioX)
	})
	reFay := regexp.MustCompile(`\\fay(-?\d+(\.\d+)?)`)
	s = reFay.ReplaceAllStringFunc(s, func(m string) string {
		sub := reFay.FindStringSubmatch(m)
		return `\fay` + scaleNumberString(sub[1], ratioY)
	})

	// \fscx \fscy \fsc (scale percent) - scale relative? We'll preserve percentages but do not convert them to pixels.
	// For safety, we will not change \fscx/\fscy (they are percentages). If you'd like, we could attempt to adjust them.

	// \t(...) nested transforms: apply scaling inside tags portion
	reT := regexp.MustCompile(`\\t\(([^)]*)\)`)
	// iterate until no change to handle nested
	for reT.MatchString(s) {
		s = reT.ReplaceAllStringFunc(s, func(m string) string {
			sub := reT.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			inner := sub[1]
			// inner may be "t1,t2, tags" or just tags. We attempt to find the tags part (starting with \)
			idx := strings.Index(inner, `\`)
			if idx >= 0 {
				prefix := inner[:idx]
				tags := inner[idx:]
				return `\t(` + prefix + scaleTags(tags, ratioX, ratioY) + `)`
			}
			// else scale anything numeric inside
			return `\t(` + scaleTags(inner, ratioX, ratioY) + `)`
		})
	}

	// done
	return s
}

// ---------- Main processing function untuk Resample ASS ----------
func processASS(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("gagal membaca file: %w", err)
	}
	text := string(raw)

	// Normalize line endings to \n
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// 1) Find PlayResX / PlayResY in [Script Info]
	rePlayResX := regexp.MustCompile(`(?mi)^\s*PlayResX\s*:\s*(\d+)\s*$`)
	rePlayResY := regexp.MustCompile(`(?mi)^\s*PlayResY\s*:\s*(\d+)\s*$`)
	origX := defaultPlayResX
	origY := defaultPlayResY

	if m := rePlayResX.FindStringSubmatch(text); len(m) >= 2 {
		origX = parseFloatSafe(m[1], defaultPlayResX)
	}
	if m := rePlayResY.FindStringSubmatch(text); len(m) >= 2 {
		origY = parseFloatSafe(m[1], defaultPlayResY)
	}
	ratioX := targetPlayResX / origX
	ratioY := targetPlayResY / origY

	// Replace or insert PlayResX / PlayResY
	if rePlayResX.MatchString(text) {
		text = rePlayResX.ReplaceAllString(text, fmt.Sprintf("PlayResX: %d", int(targetPlayResX)))
	} else {
		// insert after [Script Info] header if present, otherwise at top
		reScriptInfo := regexp.MustCompile(`(?m)^\[Script Info\]\s*$`)
		if loc := reScriptInfo.FindStringIndex(text); loc != nil {
			insertAt := loc[1]
			text = text[:insertAt] + "\nPlayResX: 1920\n" + text[insertAt:]
		} else {
			text = "[Script Info]\nPlayResX: 1920\n" + text
		}
	}
	if rePlayResY.MatchString(text) {
		text = rePlayResY.ReplaceAllString(text, fmt.Sprintf("PlayResY: %d", int(targetPlayResY)))
	} else {
		reScriptInfo := regexp.MustCompile(`(?m)^\[Script Info\]\s*$`)
		if loc := reScriptInfo.FindStringIndex(text); loc != nil {
			insertAt := loc[1]
			text = text[:insertAt] + "\nPlayResY: 1080\n" + text[insertAt:]
		} else {
			text = "[Script Info]\nPlayResY: 1080\n" + text
		}
	}

	// 2) Process [V4+ Styles] block
	lower := strings.ToLower(text)
	header := "[v4+ styles]"
	hIdx := strings.Index(lower, header)
	if hIdx != -1 {
		// find block start and end
		// get substring from header position
		sub := text[hIdx:]
		// find next section header after header
		reSection := regexp.MustCompile(`(?m)^\[.+\]`)
		locs := reSection.FindAllStringIndex(sub, -1)
		endRel := len(sub)
		if len(locs) >= 2 {
			// locs[0] == header itself; next is end
			endRel = locs[1][0]
		}
		block := sub[:endRel] // includes header line
		// process block line by line
		lines := strings.Split(block, "\n")
		formatFields := []string{}
		styleIndices := []int{} // indices in lines where Style: occurs
		for i, ln := range lines {
			lt := strings.TrimSpace(ln)
			lowerln := strings.ToLower(lt)
			if strings.HasPrefix(lowerln, "format:") {
				// capture format order
				fmtLine := strings.TrimSpace(ln[len("format:"):])
				parts := strings.Split(fmtLine, ",")
				formatFields = make([]string, 0, len(parts))
				for _, p := range parts {
					formatFields = append(formatFields, strings.ToLower(strings.TrimSpace(p)))
				}
			} else if strings.HasPrefix(lowerln, "style:") {
				styleIndices = append(styleIndices, i)
			}
		}

		// If formatFields empty, fallback to default ASS order
		if len(formatFields) == 0 {
			formatFields = []string{
				"name", "fontname", "fontsize", "primarycolour", "secondarycolour", "outlinecolour", "backcolour",
				"bold", "italic", "underline", "strikeout", "scalex", "scaley", "spacing", "angle",
				"borderstyle", "outline", "shadow", "alignment", "marginl", "marginr", "marginv", "encoding",
			}
		}

		// determine indices
		fontIdx := -1
		fsIdx := -1
		for i, f := range formatFields {
			if f == "fontname" && fontIdx == -1 {
				fontIdx = i
			}
			if f == "fontsize" && fsIdx == -1 {
				fsIdx = i
			}
		}
		// when mapping into parts, note that Style: content fields correspond to formatFields order
		// process style lines
		for _, si := range styleIndices {
			ln := lines[si]
			// preserve original prefix ("Style:" plus possibly spaces)
			prefix := ln[:strings.Index(strings.ToLower(ln), "style:")+6] // "Style:" (6 chars)
			content := strings.TrimSpace(ln[len(prefix):])
			// split into len(formatFields) parts
			parts := splitNPreserveTrailing(content, ',', len(formatFields))
			// ensure parts has length == len(formatFields)
			if len(parts) < len(formatFields) {
				// pad
				for len(parts) < len(formatFields) {
					parts = append(parts, "")
				}
			}
			// replace fontname and fontsize if indices valid
			if fontIdx >= 0 && fontIdx < len(parts) {
				parts[fontIdx] = targetFontName
			}
			if fsIdx >= 0 && fsIdx < len(parts) {
				oldFs := strings.TrimSpace(parts[fsIdx])
				if oldFs != "" {
					if fv, err := strconv.ParseFloat(oldFs, 64); err == nil {
						newFs := fv * ratioY
						parts[fsIdx] = scaleFloatFormat(newFs)
					} else {
						// if parse fail, leave as-is
					}
				}
			}
			lines[si] = "Style: " + strings.Join(parts, ",")
		}

		// Insert resStyleLine at the end of style list (i.e., after last Style: line and before next non-style in block)
		insertAt := -1
		if len(styleIndices) > 0 {
			insertAt = styleIndices[len(styleIndices)-1] + 1
		} else {
			// if no style lines, try to insert after Format: if exists, else after header line (index 0)
			foundFmt := false
			for i, ln := range lines {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ln)), "format:") {
					insertAt = i + 1
					foundFmt = true
					break
				}
			}
			if !foundFmt {
				insertAt = 1 // after header line
			}
		}
		// insert res style
		// ensure we do not duplicate if already present
		already := false
		for _, ln := range lines {
			if strings.TrimSpace(ln) == resStyleLine {
				already = true
				break
			}
		}
		if !already {
			if insertAt < 0 {
				lines = append(lines, resStyleLine)
			} else if insertAt >= len(lines) {
				lines = append(lines, resStyleLine)
			} else {
				// insert
				head := append([]string{}, lines[:insertAt]...)
				head = append(head, resStyleLine)
				head = append(head, lines[insertAt:]...)
				lines = head
			}
		}

		// reconstruct block and replace in text
		newBlock := strings.Join(lines, "\n")
		text = text[:hIdx] + newBlock + text[hIdx+len(block):]
	}

	// 3) Process [Events] block: replace \fn only if present in overrides and scale tags inside overrides
	reEventsHeader := regexp.MustCompile(`(?mi)^\[Events\]\s*$`)
	loc := reEventsHeader.FindStringIndex(text)
	if loc != nil {
		eventsStart := loc[1]
		// find next section header after eventsStart
		reSection := regexp.MustCompile(`(?m)^\[.+\]`)
		rest := text[eventsStart:]
		nexts := reSection.FindAllStringIndex(rest, -1)
		eventsBlock := ""
		eventsEndRel := len(rest)
		if len(nexts) >= 1 {
			eventsBlock = rest[:nexts[0][0]]
			eventsEndRel = nexts[0][0]
		} else {
			eventsBlock = rest
			eventsEndRel = len(rest)
		}
		lines := strings.Split(eventsBlock, "\n")
		// for each Dialogue line process
		for i, ln := range lines {
			trim := strings.TrimSpace(ln)
			if strings.HasPrefix(strings.ToLower(trim), "dialogue:") {
				// Format: Dialogue: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
				// We'll split into 9 commas then the rest as text: SplitN with 10 parts
				parts := splitNPreserveTrailing(ln, ',', 10)
				if len(parts) < 10 {
					// fallback: leave unchanged
					continue
				}
				textField := parts[9]

				// find all override blocks { ... } and process each
				reOverride := regexp.MustCompile(`\{[^}]*\}`)
				textField = reOverride.ReplaceAllStringFunc(textField, func(ov string) string {
					inside := ov[1 : len(ov)-1] // without braces
					// if has \fn, replace it (only if present)
					reFn := regexp.MustCompile(`\\fn[^\\}]+`)
					if reFn.MatchString(inside) {
						inside = reFn.ReplaceAllString(inside, `\fn`+targetFontName)
					}
					// scale tags inside override
					inside = scaleTags(inside, ratioX, ratioY)
					return "{" + inside + "}"
				})

				// Also, there might be inline \fn outside braces (rare) - but PER REQUEST, only alter if in override. So we won't change outside.

				parts[9] = textField
				// reconstruct the line using comma as separator (we used split that preserved trailing text)
				lines[i] = strings.Join(parts, ",")
			}
		}
		// reconstruct eventsBlock
		newEventsBlock := strings.Join(lines, "\n")
		// replace in original text
		prefix := text[:eventsStart]
		suffix := text[eventsStart+eventsEndRel:]
		text = prefix + newEventsBlock + suffix
	}

	// ensure trailing newline
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	return text, nil
}
//===batas resample ass===

// ======================
// TTML / Custom XML types
// ======================
type TTMLParagraph struct {
	XMLName xml.Name `xml:"p"`
	Begin   string   `xml:"begin,attr"`
	End     string   `xml:"end,attr"`
	Text    string   `xml:",innerxml"`
}

// 🔹 Struktur baru untuk TTML umum
type TTMLRoot struct {
	XMLName xml.Name `xml:"tt"`
	Body    struct {
		Div []struct {
			Paragraphs []TTMLParagraph `xml:"p"`
		} `xml:"div"`
		Paragraphs []TTMLParagraph `xml:"p"` // Untuk struktur tanpa div
	} `xml:"body"`
}

// 🔹 Struktur untuk XML format khusus (dari test file)
type CustomXMLRoot struct {
	XMLName xml.Name `xml:"xml"`
	Dia     []struct {
		ST    string `xml:"st"`  // Start time (centiseconds)
		ET    string `xml:"et"`  // End time (centiseconds)
		Sub   string `xml:"sub"` // Subtitle text (CDATA)
		Style struct {
			Position struct {
				Alignment        string `xml:"alignment,attr"`
				HorizontalMargin string `xml:"horizontal-margin,attr"`
				VerticalMargin   string `xml:"vertical-margin,attr"`
			} `xml:"position"`
		} `xml:"style"`
	} `xml:"dia"`
}

var (
	reTimeFull = regexp.MustCompile(`(\d+):(\d+):(\d+)\.(\d+)`) // HH:MM:SS.ms
	reTimeNoMS = regexp.MustCompile(`(\d+):(\d+):(\d+)`)       // HH:MM:SS
)

// ======================================
// 🔹 Helper: Deep HTML Unescape
// ======================================
func deepUnescapeHTML(s string) string {
	prev := ""
	for s != prev {
		prev = s
		s = html.UnescapeString(s)
	}
	// Handle whitespace & invisible entities that html.UnescapeString doesn't replace
	replacements := map[string]string{
		"&nbsp;":           " ",
		"&NewLine;":        "\n",
		"&thinsp;":         " ",
		"&ensp;":           " ",
		"&emsp;":           " ",
		"&ZeroWidthSpace;": "",
	}
	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// ======================================
// 🔹 Fungsi: Convert Custom XML → SRT (in-memory)
// ======================================
func convertCustomXMLtoSRT(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	// Use deep unescape to handle double-escaped and non-standard entities
	content := deepUnescapeHTML(string(data))

	var xmlRoot CustomXMLRoot
	if err := xml.Unmarshal([]byte(content), &xmlRoot); err != nil {
		return "", fmt.Errorf("gagal parse custom XML: %v", err)
	}

	if len(xmlRoot.Dia) == 0 {
		return "", fmt.Errorf("tidak ada subtitle ditemukan dalam custom XML")
	}

	var sb strings.Builder
	counter := 1

	for _, dia := range xmlRoot.Dia {
		// deep unescape also applied to inner text
		text := deepUnescapeHTML(strings.TrimSpace(dia.Sub))
		if text == "" {
			continue
		}

		// Handle line breaks dalam CDATA
		text = strings.ReplaceAll(text, "\n", "\\N")

		// Convert waktu dari centiseconds ke format SRT
		startTime := centisecondsToSRTTime(dia.ST)
		endTime := centisecondsToSRTTime(dia.ET)

		sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n",
			counter,
			startTime,
			endTime,
			text))
		counter++
	}

	if counter == 1 {
		return "", fmt.Errorf("tidak ada subtitle yang valid ditemukan dalam custom XML")
	}

	return sb.String(), nil
}

// ======================================
// 🔹 Helper: Convert centiseconds to SRT time
// ======================================
func centisecondsToSRTTime(cs string) string {
	centiseconds, err := strconv.Atoi(cs)
	if err != nil {
		return "00:00:00,000"
	}

	// Convert centiseconds to milliseconds
	milliseconds := centiseconds * 10

	hours := milliseconds / 3600000
	milliseconds %= 3600000

	minutes := milliseconds / 60000
	milliseconds %= 60000

	seconds := milliseconds / 1000
	milliseconds %= 1000

	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, milliseconds)
}

// ======================================
// 🔹 Fungsi: Convert VTT → SRT (in-memory)
// ======================================
func convertVTTtoSRT(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	// deep unescape for VTT content too
	content := deepUnescapeHTML(string(data))
	lines := strings.Split(content, "\n")

	var sb strings.Builder
	counter := 1
	i := 0

	// Skip WEBVTT header dan metadata
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "WEBVTT") {
			i++
			// Skip metadata lines setelah WEBVTT
			for i < len(lines) && strings.Contains(lines[i], ":") {
				i++
			}
			break
		}
		i++
	}

	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}

		// Skip cue identifiers (biasanya angka atau teks di atas timing)
		if !strings.Contains(line, "-->") && i+1 < len(lines) && strings.Contains(lines[i+1], "-->") {
			i++ // Skip identifier line
			continue
		}

		// Cek jika line mengandung timing (-->)
		if strings.Contains(line, "-->") {
			// Parse timing line
			timingParts := strings.Split(line, " --> ")
			if len(timingParts) != 2 {
				i++
				continue
			}

			startTime := vttTimeToSRT(timingParts[0])
			endTime := vttTimeToSRT(timingParts[1])

			i++
			var textLines []string

			// Kumpulkan teks subtitle
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				// apply deep unescape to each subtitle text line
				text := deepUnescapeHTML(strings.TrimSpace(lines[i]))
				// Handle VTT tags
				text = vttTagsToSRT(text)
				if text != "" {
					textLines = append(textLines, text)
				}
				i++
			}

			if len(textLines) > 0 {
				fullText := strings.Join(textLines, "\n")
				sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n",
					counter, startTime, endTime, fullText))
				counter++
			}
		} else {
			i++
		}
	}

	if counter == 1 {
		return "", fmt.Errorf("tidak ada subtitle VTT yang valid ditemukan")
	}

	return sb.String(), nil
}

// ======================================
// 🔹 Helper: VTT time → SRT time
// ======================================
func vttTimeToSRT(t string) string {
	// Format VTT: HH:MM:SS.ms atau MM:SS.ms
	t = strings.TrimSpace(t)

	// Handle kemungkinan adanya cue settings setelah waktu
	parts := strings.Fields(t)
	if len(parts) > 0 {
		t = parts[0]
	}

	// Coba format dengan milliseconds: HH:MM:SS.ms
	if matches := reTimeFull.FindStringSubmatch(t); len(matches) >= 5 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		ms, _ := strconv.Atoi(matches[4])
		return fmt.Sprintf("%02d:%02d:%02d,%03d", h, min, sec, ms)
	}

	// Coba format tanpa hours: MM:SS.ms
	reShortTime := regexp.MustCompile(`(\d+):(\d+)\.(\d+)`)
	if matches := reShortTime.FindStringSubmatch(t); len(matches) >= 4 {
		min, _ := strconv.Atoi(matches[1])
		sec, _ := strconv.Atoi(matches[2])
		ms, _ := strconv.Atoi(matches[3])
		// Convert ke format dengan hours
		h := min / 60
		min = min % 60
		return fmt.Sprintf("%02d:%02d:%02d,%03d", h, min, sec, ms)
	}

	// Coba format tanpa milliseconds: HH:MM:SS
	if matches := reTimeNoMS.FindStringSubmatch(t); len(matches) >= 4 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		return fmt.Sprintf("%02d:%02d:%02d,000", h, min, sec)
	}

	return "00:00:00,000"
}

// ======================================
// 🔹 Helper: Convert VTT tags to SRT compatible
// ======================================
func vttTagsToSRT(text string) string {
	// Convert VTT cue tags to HTML-like tags untuk kompatibilitas
	text = regexp.MustCompile(`<(\d{2}:\d{2}:\d{2}\.\d{3})>`).ReplaceAllString(text, "") // Remove timestamp tags

	// Convert voice tags <v Speaker> menjadi "Speaker: "
	text = regexp.MustCompile(`<v\s+([^>]+)>`).ReplaceAllString(text, "$1: ")
	text = strings.ReplaceAll(text, "</v>", "")

	// Convert Ruby tags (umum di VTT)
	text = regexp.MustCompile(`<ruby>([^<]*)<rt>([^<]*)</rt></ruby>`).ReplaceAllString(text, "$1")

	// Convert color tags: <c.color> -> <font color="color">
	text = regexp.MustCompile(`<c\.(#[0-9A-Fa-f]{6})>`).ReplaceAllString(text, `<font color="$1">`)
	text = strings.ReplaceAll(text, "</c>", "</font>")

	// Convert class tags: <c.class> -> simple text (remove tags)
	text = regexp.MustCompile(`<c\.[^>]*>`).ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "</c>", "")

	// Bold, Italic, Underline - VTT menggunakan sama seperti HTML
	text = strings.ReplaceAll(text, "<b>", "<b>")
	text = strings.ReplaceAll(text, "</b>", "</b>")
	text = strings.ReplaceAll(text, "<i>", "<i>")
	text = strings.ReplaceAll(text, "</i>", "</i>")
	text = strings.ReplaceAll(text, "<u>", "<u>")
	text = strings.ReplaceAll(text, "</u>", "</u>")

	return text
}

// ======================================
// 🔹 Fungsi: Convert TTML → SRT (in-memory, versi kuat)
// ======================================
func convertTTMLtoSRT(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	// Deep unescape
	content := deepUnescapeHTML(string(data))

	// 🔹 PARSING TTML UMUM - Coba struktur TTML standar dulu
	var ttmlRoot TTMLRoot
	if err := xml.Unmarshal([]byte(content), &ttmlRoot); err == nil {
		var paragraphs []TTMLParagraph

		// Kumpulkan semua paragraf dari berbagai struktur
		for _, div := range ttmlRoot.Body.Div {
			paragraphs = append(paragraphs, div.Paragraphs...)
		}
		paragraphs = append(paragraphs, ttmlRoot.Body.Paragraphs...)

		if len(paragraphs) > 0 {
			return buildSRTFromParagraphs(paragraphs)
		}
	}

	// 🔹 FALLBACK 1: Parsing XML: coba struktur umum <body><div><p>
	var root struct {
		Paragraphs []TTMLParagraph `xml:"body>div>p"`
	}
	if err := xml.Unmarshal([]byte(content), &root); err == nil && len(root.Paragraphs) > 0 {
		return buildSRTFromParagraphs(root.Paragraphs)
	}

	// 🔹 FALLBACK 2: struktur <body><p>
	var alt struct {
		Paragraphs []TTMLParagraph `xml:"body>p"`
	}
	if err := xml.Unmarshal([]byte(content), &alt); err == nil && len(alt.Paragraphs) > 0 {
		return buildSRTFromParagraphs(alt.Paragraphs)
	}

	// 🔹 FALLBACK 3: Cari semua tag <p> di mana saja dalam dokumen
	var allParagraphs struct {
		Paragraphs []TTMLParagraph `xml:"p"`
	}
	if err := xml.Unmarshal([]byte(content), &allParagraphs); err == nil && len(allParagraphs.Paragraphs) > 0 {
		return buildSRTFromParagraphs(allParagraphs.Paragraphs)
	}

	return "", fmt.Errorf("gagal parse TTML: tidak ditemukan struktur yang dikenali")
}

// ======================================
// 🔹 Helper: Build SRT dari paragraphs
// ======================================
func buildSRTFromParagraphs(paragraphs []TTMLParagraph) (string, error) {
	var sb strings.Builder
	counter := 1

	for _, p := range paragraphs {
		text := p.Text
		text = strings.ReplaceAll(text, "<br/>", "\n")
		text = strings.ReplaceAll(text, "<br />", "\n")
		text = strings.ReplaceAll(text, "<br>", "\n")
		// apply deep unescape to paragraph text (handles CDATA / nested entities)
		text = deepUnescapeHTML(text)
		text = stripHTMLTags(text)
		text = strings.TrimSpace(text)

		if text == "" {
			continue
		}

		// Pastikan waktu valid
		startTime := ttmlTimeToSRT(p.Begin)
		endTime := ttmlTimeToSRT(p.End)

		sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n",
			counter,
			startTime,
			endTime,
			text))
		counter++
	}

	if counter == 1 {
		return "", fmt.Errorf("tidak ada subtitle yang valid ditemukan")
	}

	return sb.String(), nil
}

// ======================================
// 🔹 Helper: TTML time → SRT time (DIPERBAIKI)
// ======================================
func ttmlTimeToSRT(t string) string {
	// Coba format dengan milliseconds dulu: HH:MM:SS.ms
	if matches := reTimeFull.FindStringSubmatch(t); len(matches) >= 5 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		ms, _ := strconv.Atoi(matches[4])
		return fmt.Sprintf("%02d:%02d:%02d,%03d", h, min, sec, ms)
	}

	// Coba format tanpa milliseconds: HH:MM:SS
	if matches := reTimeNoMS.FindStringSubmatch(t); len(matches) >= 4 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		return fmt.Sprintf("%02d:%02d:%02d,000", h, min, sec)
	}

	// Coba format frames (00:00:00:00)
	reFrames := regexp.MustCompile(`(\d+):(\d+):(\d+):(\d+)`)
	if matches := reFrames.FindStringSubmatch(t); len(matches) >= 5 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		frames, _ := strconv.Atoi(matches[4])
		// Asumsi 25 fps untuk konversi frame ke ms
		ms := frames * 40
		return fmt.Sprintf("%02d:%02d:%02d,%03d", h, min, sec, ms)
	}

	// Coba format timecode dengan hours pendek (H:MM:SS.ms)
	reShortTime := regexp.MustCompile(`(\d+):(\d+):(\d+)\.(\d+)`)
	if matches := reShortTime.FindStringSubmatch(t); len(matches) >= 5 {
		h, _ := strconv.Atoi(matches[1])
		min, _ := strconv.Atoi(matches[2])
		sec, _ := strconv.Atoi(matches[3])
		ms, _ := strconv.Atoi(matches[4])
		return fmt.Sprintf("%02d:%02d:%02d,%03d", h, min, sec, ms)
	}

	// Default fallback
	return "00:00:00,000"
}

// ======================================
// 🔹 Helper: hapus semua tag HTML tapi pertahankan \n
// ======================================
func stripHTMLTags(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	re := regexp.MustCompile(`(?i)</?[^>]+>`)
	return re.ReplaceAllString(s, "")
}

// ======================================
// 🔹 Fungsi utama: proses SRT ke ASS
// ======================================
func processSRT(input interface{}) string {
	// [Kode processSRT tetap sama persis...]
	var content []byte
	switch v := input.(type) {
	case string:
		if strings.Contains(v, "\n") {
			content = []byte(v)
		} else {
			data, err := os.ReadFile(v)
			if err != nil {
				panic(err)
			}
			content = data
		}
	default:
		panic("input tidak valid untuk processSRT()")
	}

	reFontOpen := regexp.MustCompile(`(?i)<font[^>]*>`)
	reFontClose := regexp.MustCompile(`(?i)</font>`)
	reBOpen := regexp.MustCompile(`(?i)<b>`)
	reBClose := regexp.MustCompile(`(?i)</b>`)
	reIOpen := regexp.MustCompile(`(?i)<i>`)
	reIClose := regexp.MustCompile(`(?i)</i>`)
	reUOpen := regexp.MustCompile(`(?i)<u>`)
	reUClose := regexp.MustCompile(`(?i)</u>`)
	reSOpen := regexp.MustCompile(`(?i)<s>`)
	reSClose := regexp.MustCompile(`(?i)</s>`)
	reAnyTag := regexp.MustCompile(`(?i)</?[^>]+>`)
	reTiming := regexp.MustCompile(`(\d+):(\d+):(\d+),(\d+)`)

	type Dialogue struct {
		Start, End string
		Style      string
		Text       string
	}

	srtTimeToASSTime := func(s string) string {
		matches := reTiming.FindStringSubmatch(s)
		if len(matches) < 5 {
			return "0:00:00.00"
		}
		h, _ := strconv.Atoi(matches[1])
		m, _ := strconv.Atoi(matches[2])
		si, _ := strconv.Atoi(matches[3])
		ms, _ := strconv.Atoi(matches[4])
		return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, si, ms/10)
	}

	extractColorAttr := func(s string) string {
		s = strings.ToLower(s)
		if strings.Contains(s, "color=") {
			idx := strings.Index(s, "color=")
			after := s[idx+6:]
			after = strings.TrimLeft(after, " \t")
			if len(after) == 0 {
				return ""
			}
			if after[0] == '"' || after[0] == '\'' {
				q := after[0]
				after = after[1:]
				end := strings.IndexRune(after, rune(q))
				if end != -1 {
					return after[:end]
				}
			} else {
				fields := strings.Fields(after)
				return strings.Trim(fields[0], ">")
			}
		}
		return ""
	}

	convertTagsToASS := func(text string) string {
		text = reFontOpen.ReplaceAllStringFunc(text, func(m string) string {
			color := extractColorAttr(m)
			if color != "" {
				c := strings.TrimPrefix(color, "#")
				if len(c) == 6 {
					rr := c[0:2]
					gg := c[2:4]
					bb := c[4:6]
					return fmt.Sprintf("{\\c&H%s%s%s&}", bb, gg, rr)
				}
			}
			return ""
		})
		text = reFontClose.ReplaceAllString(text, "")
		text = regexp.MustCompile(`\{\\f[ns][^}]*\}`).ReplaceAllString(text, "")
		text = reBOpen.ReplaceAllString(text, "{\\b1}")
		text = reBClose.ReplaceAllString(text, "{\\b0}")
		text = reIOpen.ReplaceAllString(text, "{\\i1}")
		text = reIClose.ReplaceAllString(text, "{\\i0}")
		text = reUOpen.ReplaceAllString(text, "{\\u1}")
		text = reUClose.ReplaceAllString(text, "{\\u0}")
		text = reSOpen.ReplaceAllString(text, "{\\s1}")
		text = reSClose.ReplaceAllString(text, "{\\s0}")
		text = reAnyTag.ReplaceAllString(text, "")
		text = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(text, " "))
		return text
	}

	defineStyle := func(text string) string {
		clean := regexp.MustCompile(`(?i)\{\\[^}]+\}`).ReplaceAllString(text, "")
		clean = strings.TrimSpace(clean)
		if (strings.HasPrefix(clean, "(") && strings.HasSuffix(clean, ")")) ||
			(strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]")) {
			return "tanda"
		}
		alpha := regexp.MustCompile(`[A-Z0-9\s[:punct:]]+$`)
		if alpha.MatchString(clean) && strings.ToUpper(clean) == clean {
			return "tanda"
		}
		return "Default"
	}

	lines := strings.Split(string(content), "\n")
	var dialogs []Dialogue
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if reTiming.MatchString(line) {
			timeParts := strings.Split(line, " --> ")
			start := srtTimeToASSTime(timeParts[0])
			end := srtTimeToASSTime(timeParts[1])
			i++
			var textLines []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				textLines = append(textLines, lines[i])
				i++
			}
			for _, t := range textLines {
				dialog := Dialogue{
					Start: start,
					End:   end,
					Text:  convertTagsToASS(t),
				}
				dialog.Style = defineStyle(dialog.Text)
				dialogs = append(dialogs, dialog)
			}
		} else {
			i++
		}
	}

	sort.Slice(dialogs, func(i, j int) bool {
		if dialogs[i].Start == dialogs[j].Start {
			if dialogs[i].End == dialogs[j].End {
				return dialogs[i].Style < dialogs[j].Style
			}
			return dialogs[i].End < dialogs[j].End
		}
		return dialogs[i].Start < dialogs[j].Start
	})

	var merged []Dialogue
	for i := 0; i < len(dialogs); i++ {
		curr := dialogs[i]
		for j := i + 1; j < len(dialogs); j++ {
			next := dialogs[j]
			if curr.Style == next.Style && curr.Start == next.Start && curr.End == next.End {
				if curr.Text != next.Text {
					curr.Text += `\N` + next.Text
				}
				dialogs[j].Style = "__merged__"
			} else if curr.Style == next.Style && curr.Text == next.Text && curr.End == next.Start {
				curr.End = next.End
				dialogs[j].Style = "__merged__"
			}
		}
		if curr.Style != "__merged__" {
			merged = append(merged, curr)
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Style == "tanda" && merged[j].Style != "tanda" {
			return true
		}
		if merged[i].Style != "tanda" && merged[j].Style == "tanda" {
			return false
		}
		return merged[i].Start < merged[j].Start
	})

	header := `[Script Info]
; Script generated by Limesub v3
; https://t.me/s/limenime
; https://www.facebook.com/limenime.official
; https://discord.gg/7XS7MCvVwh
; https://x.com/limenime
Title: Default Limenime Subtitle File
ScriptType: v4.00+
WrapStyle: 0
ScaledBorderAndShadow: yes
YCbCr Matrix: None
PlayResX: 1920
PlayResY: 1080
Timer: 100.0000

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Basic Comical NC,70,&H00FFFFFF,&H00FFFFFF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,1.5,1,2,64,64,33,1
Style: Default Above,Basic Comical NC,70,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,1.5,1,8,0,0,65,1
Style: res,Basic Comical NC,1080,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,0,0,0,0,1,2,2,2,10,10,10,1
Style: tanda,Basic Comical NC,75,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,1,0,8,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text`

	var sb strings.Builder
	sb.WriteString(header + "\n")
	for _, d := range merged {
		text := d.Text
		if d.Style == "Default" {
			text = "{\\blur3}{\\fad(00,40)}" + text
		}
		sb.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,%s,,0000,0000,0000,,%s\n",
			d.Start, d.End, d.Style, text))
	}
	return sb.String()
}

// ======================================
// 🔹 JSON parsers & detection (Bilibili & YouTube)
// ======================================

// Bili JSON structure (common)
type biliBodyEntry struct {
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	Location int     `json:"location,omitempty"`
	Content  string  `json:"content"`
}
type biliJSON struct {
	Body []biliBodyEntry `json:"body"`
}

// YouTube JSON structure (common shape)
type ytSeg struct {
	UTF8 string `json:"utf8"`
}
type ytEvent struct {
	TStartMs   float64 `json:"tStartMs"`   // can be integer or float in JSON -> use float64
	DDurationMs float64 `json:"dDurationMs"` // duration in ms
	Segs       []ytSeg `json:"segs"`
}
type ytJSON struct {
	Events []ytEvent `json:"events"`
}

// convertJSONtoSRT: baca file .json, deteksi format, kembalikan string SRT
func convertJSONtoSRT(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))

	// Quick detection based on keys
	lower := strings.ToLower(text)
	if strings.Contains(lower, `"body"`) && (strings.Contains(lower, `"from"`) || strings.Contains(lower, `"content"`)) {
		// Bilibili-like
		var b biliJSON
		if err := json.Unmarshal(data, &b); err != nil {
			// fallback: try to decode ignoring unknown fields
			return "", fmt.Errorf("gagal parse JSON Bilibili: %v", err)
		}
		var sb strings.Builder
		counter := 1
		for _, it := range b.Body {
			// Guard: ensure valid times
			start := it.From
			end := it.To
			if end <= 0 || end <= start {
				// skip invalid entry
				continue
			}
			startS := formatTime(start)
			endS := formatTime(end)
			// replace newlines with SRT linebreaks
			content := strings.ReplaceAll(strings.TrimSpace(it.Content), "\n", "\n")
			sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n", counter, startS, endS, content))
			counter++
		}
		if sb.Len() == 0 {
			return "", fmt.Errorf("tidak ada caption valid ditemukan di Bilibili JSON")
		}
		return sb.String(), nil
	} else if strings.Contains(lower, `"events"`) && strings.Contains(lower, `"tstartms"`) {
		// YouTube-like
		var y ytJSON
		if err := json.Unmarshal(data, &y); err != nil {
			return "", fmt.Errorf("gagal parse JSON YouTube: %v", err)
		}
		type caption struct {
			Start float64
			End   float64
			Text  string
		}
		var caps []caption
		for _, ev := range y.Events {
			if len(ev.Segs) == 0 {
				continue
			}
			start := ev.TStartMs / 1000.0
			end := (ev.TStartMs + ev.DDurationMs) / 1000.0
			parts := make([]string, 0, len(ev.Segs))
			for _, s := range ev.Segs {
				parts = append(parts, strings.TrimSpace(s.UTF8))
			}
			txt := strings.Join(parts, "")
			// skip empty
			if strings.TrimSpace(txt) == "" {
				continue
			}
			caps = append(caps, caption{Start: start, End: end, Text: txt})
		}
		if len(caps) == 0 {
			return "", fmt.Errorf("tidak ada caption valid ditemukan di YouTube JSON")
		}
		// sort by start
		sort.Slice(caps, func(i, j int) bool { return caps[i].Start < caps[j].Start })
		var sb strings.Builder
		for i, c := range caps {
			sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n", i+1, formatTime(c.Start), formatTime(c.End), strings.ReplaceAll(c.Text, "\n", "\n")))
		}
		return sb.String(), nil
	}

	// If not matched, attempt to decode generically:
	var probe map[string]interface{}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("format JSON tidak dikenali dan gagal decode: %v", err)
	}
	// try search keys
	if _, ok := probe["body"]; ok {
		// try to unmarshal as bili
		var b biliJSON
		if err := json.Unmarshal(data, &b); err == nil && len(b.Body) > 0 {
			var sb strings.Builder
			counter := 1
			for _, it := range b.Body {
				startS := formatTime(it.From)
				endS := formatTime(it.To)
				sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n", counter, startS, endS, strings.TrimSpace(it.Content)))
				counter++
			}
			if sb.Len() > 0 {
				return sb.String(), nil
			}
		}
	}
	if _, ok := probe["events"]; ok {
		var y ytJSON
		if err := json.Unmarshal(data, &y); err == nil && len(y.Events) > 0 {
			type caption struct {
				Start float64
				End   float64
				Text  string
			}
			var caps []caption
			for _, ev := range y.Events {
				if len(ev.Segs) == 0 {
					continue
				}
				start := ev.TStartMs / 1000.0
				end := (ev.TStartMs + ev.DDurationMs) / 1000.0
				parts := make([]string, 0, len(ev.Segs))
				for _, s := range ev.Segs {
					parts = append(parts, strings.TrimSpace(s.UTF8))
				}
				txt := strings.Join(parts, "")
				if strings.TrimSpace(txt) == "" {
					continue
				}
				caps = append(caps, caption{Start: start, End: end, Text: txt})
			}
			sort.Slice(caps, func(i, j int) bool { return caps[i].Start < caps[j].Start })
			var sb strings.Builder
			for i, c := range caps {
				sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n", i+1, formatTime(c.Start), formatTime(c.End), strings.TrimSpace(c.Text)))
			}
			if sb.Len() > 0 {
				return sb.String(), nil
			}
		}
	}

	return "", fmt.Errorf("format JSON tidak dikenali atau tidak ada caption")
}

// formatTime: seconds (float) -> SRT timestamp (HH:MM:SS,mmm)
func formatTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMs := int(seconds*1000 + 0.5)
	h := totalMs / 3600000
	totalMs %= 3600000
	m := totalMs / 60000
	totalMs %= 60000
	s := totalMs / 1000
	ms := totalMs % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func safeDialogMessage(title, msg string, isError bool) {
	defer func() {
		if r := recover(); r != nil {
			// fallback ke terminal jika library dialog gagal
			if isError {
				fmt.Fprintf(os.Stderr, "\n[%s] %s\n", title, msg)
			} else {
				fmt.Printf("\n[%s] %s\n", title, msg)
			}
		}
	}()

	if isError {
		dialog.Message(msg).Title(title).Error()
	} else {
		dialog.Message(msg).Title(title).Info()
	}
}


// ======================================
// Entry point utama
// ======================================
func main() {
	defer func() {
		if r := recover(); r != nil {
			safeDialogMessage("Limesub v3 - Error",
				fmt.Sprintf("Terjadi kesalahan tak terduga:\n\n%v", r),
				true)
		}
	}()

	if len(os.Args) < 2 {
		safeDialogMessage("Limesub v3 - Informasi",
			"Program ini hanya dapat dijalankan dengan cara:\n\n👉 Drag & drop file subtitle ke ikon program, atau\n👉 Jalankan melalui Command Line Interface (CLI).",
			true)
		return
	}

	input := os.Args[1]
	ext := strings.ToLower(filepath.Ext(input))

	var srtData string
	var err error
	var output string

	switch ext {
	case ".ttml", ".xml":
		srtData, err = convertCustomXMLtoSRT(input)
		if err != nil {
			srtData, err = convertTTMLtoSRT(input)
			if err != nil {
				safeDialogMessage("Limesub v3 - Error",
					fmt.Sprintf("Gagal memproses file XML/TTML:\n\n%v", err),
					true)
				return
			}
		}
		output = generateOutputName(input)
		result := processSRT(srtData)
		err = os.WriteFile(output, []byte(result), 0644)

	case ".vtt":
		srtData, err = convertVTTtoSRT(input)
		if err != nil {
			safeDialogMessage("Limesub v3 - Error",
				fmt.Sprintf("Gagal memproses file VTT:\n\n%v", err),
				true)
			return
		}
		output = generateOutputName(input)
		result := processSRT(srtData)
		err = os.WriteFile(output, []byte(result), 0644)

	case ".srt":
		data, _ := os.ReadFile(input)
		srtData = string(data)
		output = generateOutputName(input)
		result := processSRT(srtData)
		err = os.WriteFile(output, []byte(result), 0644)

	case ".json":
		srtData, err = convertJSONtoSRT(input)
		if err != nil {
			safeDialogMessage("Limesub v3 - Error",
				fmt.Sprintf("Gagal memproses file JSON:\n\n%v", err),
				true)
			return
		}
		output = generateOutputName(input)
		result := processSRT(srtData)
		err = os.WriteFile(output, []byte(result), 0644)

	case ".ass":
		srtData, err = processASS(input)
		if err != nil {
			safeDialogMessage("Limesub v3 - Error",
				fmt.Sprintf("Gagal memproses file ASS:\n\n%v", err),
				true)
			return
		}
		output = generateOutputName(input)
		err = os.WriteFile(output, []byte(srtData), 0644)

	default:
		safeDialogMessage("Limesub v3 - Format Tidak Didukung",
			"Format file ini belum didukung.\n\nGunakan file dengan ekstensi .srt, .vtt, .ttml, .xml, .json, atau .ass.",
			true)
		return
	}

	if err != nil {
		safeDialogMessage("Limesub v3 - Error",
			fmt.Sprintf("Terjadi kesalahan saat menulis output:\n\n%v", err),
			true)
		return
	}
	fmt.Sprintf("✅ Konversi selesai!\n\nFile berhasil disimpan sebagai:\n%s", output)
}

// ======================================
// 🔹 Penamaan file otomatis
// ======================================
func generateOutputName(input string) string {
	base := strings.TrimSuffix(input, filepath.Ext(input))
	out := base + "_Limenime.ass"
	count := 1
	for {
		if _, err := os.Stat(out); os.IsNotExist(err) {
			break
		}
		out = fmt.Sprintf("%s_Limenime(%d).ass", base, count)
		count++
	}
	return out
}
