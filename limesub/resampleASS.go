package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

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

// ---------- Main processing function ----------
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

// ---------- main ----------
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: resampleASS <path_file.ass>")
		os.Exit(1)
	}
	inputPath := os.Args[1]
	ext := strings.ToLower(filepath.Ext(inputPath))
	switch ext {
	case ".ass":
		fmt.Println("Memproses file .ass:", inputPath)
		out, err := processASS(inputPath)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_resampled.ass"
		if err := os.WriteFile(outputPath, []byte(out), 0644); err != nil {
			fmt.Println("Gagal menyimpan output:", err)
			os.Exit(1)
		}
		fmt.Println("Berhasil disimpan:", outputPath)
	default:
		fmt.Printf("Format file %s belum didukung.\n", ext)
	}
}
