package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// processASS membaca file .ass, meresample sesuai target (set di dalam fungsi),
// dan mengembalikan hasil .ass yang sudah di-resample sebagai string.
// Fungsi ini berfokus pada akurasi meniru Aegisub (precision=3, rm = sqrt(rx*ry), integer rounding untuk fontsize/margins).
func processASS(inputPath string) string {
	// ---------- CONFIG: target resolution ----------
	targetW := 1920
	targetH := 1080
	// ------------------------------------------------

	raw, err := ioutil.ReadFile(inputPath)
	if err != nil {
		panic(err)
	}

	// normalize newlines for parsing
	txt := strings.ReplaceAll(string(raw), "\r\n", "\n")
	txt = strings.ReplaceAll(txt, "\r", "\n")
	lines := strings.Split(txt, "\n")

	// first pass: detect PlayResX / PlayResY
	rePlayRes := regexp.MustCompile(`(?i)^\s*PlayRes(X|Y)\s*:\s*(\d+)\s*$`)
	playResX := 0
	playResY := 0
	for _, l := range lines {
		if m := rePlayRes.FindStringSubmatch(l); len(m) == 3 {
			if strings.ToLower(m[1]) == "x" {
				playResX, _ = strconv.Atoi(m[2])
			} else {
				playResY, _ = strconv.Atoi(m[2])
			}
		}
	}

	// If PlayRes not present or zero, return original content with BOM+CRLF (Aegisub-like)
	if playResX == 0 || playResY == 0 {
		return addBOMAndCRLF(string(raw))
	}

	// calculate scale factors (Aegisub)
	rx := float64(targetW) / float64(playResX)
	ry := float64(targetH) / float64(playResY)
	rm := math.Sqrt(rx * ry)        // geometric mean for ABSOLUTE_SIZE_XY
	ar := rx / ry                   // horizontal aspect factor (applied to style.scalex)
	precision := 3                  // matches utils.cpp default usage

	// helpers: float->string matches utils.cpp: precision digits, trim trailing zeros
	floatToString := func(val float64, prec int) string {
		fmtStr := "%." + strconv.Itoa(prec) + "f"
		s := fmt.Sprintf(fmtStr, val)
		// find last non-zero
		pos := strings.LastIndexFunc(s, func(r rune) bool { return r != '0' })
		// ensure we don't remove the dot entirely; mimic utils.cpp logic
		dot := strings.Index(s, ".")
		if pos == -1 {
			// all zeros? e.g., "0.000"
			if dot >= 0 {
				return s[:dot]
			}
			return s
		}
		if pos == dot {
			// keep single zero after dot? utils.cpp advanced check:
			// in original: pos = s.find_last_not_of("0"); if (pos != s.find(".")) ++pos; s.erase(pos..end)
			// replicate:
			if pos != dot {
				pos++
			} else {
				// pos == dot -> keep integer part only
			}
		} else {
			pos++
		}
		if dot >= 0 && pos <= dot {
			// remove dot as well
			return s[:dot]
		}
		if pos > len(s) {
			return s
		}
		return s[:pos]
	}

	// number finder for path parsing
	reNumber := regexp.MustCompile(`-?\d+(\.\d+)?`)

	// scale numeric coordinates in vector path alternating x/y
	scalePathCoords := func(s string, sx, sy float64) string {
		var buf bytes.Buffer
		last := 0
		idx := 0
		locs := reNumber.FindAllStringIndex(s, -1)
		for _, loc := range locs {
			buf.WriteString(s[last:loc[0]])
			numStr := s[loc[0]:loc[1]]
			f, _ := strconv.ParseFloat(numStr, 64)
			if idx%2 == 0 {
				buf.WriteString(floatToString(f*sx, precision))
			} else {
				buf.WriteString(floatToString(f*sy, precision))
			}
			last = loc[1]
			idx++
		}
		buf.WriteString(s[last:])
		return buf.String()
	}

	// CSV split preserving empty fields (ASS uses commas)
	splitCSVPreserve := func(s string) []string {
		return strings.Split(s, ",")
	}

	// process override content inside braces {...}
	var processOverrides func(content string) string
	processOverrides = func(content string) string {
		// handle nested/simple \t(...) first (non-recursive matching for nested, but we loop)
		reT := regexp.MustCompile(`(?i)\\t\(([^()]*)\)`)
		for {
			if !reT.MatchString(content) {
				break
			}
			content = reT.ReplaceAllStringFunc(content, func(m string) string {
				g := reT.FindStringSubmatch(m)
				inner := g[1]
				processedInner := processOverrides(inner)
				return `\t(` + processedInner + `)`
			})
		}

		// \pos(x,y)
		rePos := regexp.MustCompile(`(?i)\\pos\(\s*([^)]+?)\s*\)`)
		content = rePos.ReplaceAllStringFunc(content, func(m string) string {
			g := rePos.FindStringSubmatch(m)
			parts := strings.Split(g[1], ",")
			if len(parts) >= 2 {
				x, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				y, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				return `\pos(` + floatToString(x*rx, precision) + `,` + floatToString(y*ry, precision) + `)`
			}
			return m
		})

		// \move(...)
		reMove := regexp.MustCompile(`(?i)\\move\(\s*([^)]+?)\s*\)`)
		content = reMove.ReplaceAllStringFunc(content, func(m string) string {
			g := reMove.FindStringSubmatch(m)
			parts := strings.Split(g[1], ",")
			for i := 0; i < len(parts); i++ {
				parts[i] = strings.TrimSpace(parts[i])
				if f, err := strconv.ParseFloat(parts[i], 64); err == nil {
					if i%2 == 0 {
						parts[i] = floatToString(f*rx, precision)
					} else {
						parts[i] = floatToString(f*ry, precision)
					}
				}
			}
			return `\move(` + strings.Join(parts, ",") + `)`
		})

		// \org(x,y)
		reOrg := regexp.MustCompile(`(?i)\\org\(\s*([^)]+?)\s*\)`)
		content = reOrg.ReplaceAllStringFunc(content, func(m string) string {
			g := reOrg.FindStringSubmatch(m)
			parts := strings.Split(g[1], ",")
			if len(parts) >= 2 {
				x, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				y, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				return `\org(` + floatToString(x*rx, precision) + `,` + floatToString(y*ry, precision) + `)`
			}
			return m
		})

		// \bord (use ry as in Aegisub code for outline/shadow/spacing scaling)
		reBord := regexp.MustCompile(`(?i)\\bord(-?\d+(\.\d+)?)`)
		content = reBord.ReplaceAllStringFunc(content, func(m string) string {
			g := reBord.FindStringSubmatch(m)
			v, _ := strconv.ParseFloat(g[1], 64)
			return `\bord` + floatToString(v*ry, precision)
		})

		// \shad
		reShad := regexp.MustCompile(`(?i)\\shad(-?\d+(\.\d+)?)`)
		content = reShad.ReplaceAllStringFunc(content, func(m string) string {
			g := reShad.FindStringSubmatch(m)
			v, _ := strconv.ParseFloat(g[1], 64)
			return `\shad` + floatToString(v*ry, precision)
		})

		// directional borders/shadows
		reXB := regexp.MustCompile(`(?i)\\xbord(-?\d+(\.\d+)?)`)
		reYB := regexp.MustCompile(`(?i)\\ybord(-?\d+(\.\d+)?)`)
		reXS := regexp.MustCompile(`(?i)\\xshad(-?\d+(\.\d+)?)`)
		reYS := regexp.MustCompile(`(?i)\\yshad(-?\d+(\.\d+)?)`)

		content = reXB.ReplaceAllStringFunc(content, func(m string) string {
			v := reXB.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			return `\xbord` + floatToString(f*rx, precision)
		})
		content = reYB.ReplaceAllStringFunc(content, func(m string) string {
			v := reYB.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			return `\ybord` + floatToString(f*ry, precision)
		})
		content = reXS.ReplaceAllStringFunc(content, func(m string) string {
			v := reXS.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			return `\xshad` + floatToString(f*rx, precision)
		})
		content = reYS.ReplaceAllStringFunc(content, func(m string) string {
			v := reYS.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			return `\yshad` + floatToString(f*ry, precision)
		})

		// \fs (font size override) -> scale by ry (Aegisub uses ry for fontsize)
		reFS := regexp.MustCompile(`(?i)\\fs(-?\d+(\.\d+)?)`)
		content = reFS.ReplaceAllStringFunc(content, func(m string) string {
			v := reFS.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			// In Aegisub, fs becomes integer scaled by ry + 0.5; but override often kept float in tags.
			// We'll format with floatToString to mimic internal usage.
			return `\fs` + floatToString(f*ry, precision)
		})

		// \pbo (vertical baseline offset for vector drawing) -> scale by ry
		rePbo := regexp.MustCompile(`(?i)\\pbo(-?\d+(\.\d+)?)`)
		content = rePbo.ReplaceAllStringFunc(content, func(m string) string {
			v := rePbo.FindStringSubmatch(m)[1]
			f, _ := strconv.ParseFloat(v, 64)
			return `\pbo` + floatToString(f*ry, precision)
		})

		// \clip(...) or \iclip(...)
		reClip := regexp.MustCompile(`(?i)\\i?clip\(\s*([^)]+?)\s*\)`)
		content = reClip.ReplaceAllStringFunc(content, func(m string) string {
			g := reClip.FindStringSubmatch(m)
			inner := g[1]
			if strings.ContainsAny(inner, "mlbnsMLBNS") {
				// vector path: scale path coords
				newInner := scalePathCoords(inner, rx, ry)
				prefix := m[:strings.Index(m, "(")]
				return prefix + "(" + newInner + ")"
			}
			// rectangle numbers (comma separated)
			parts := splitCSVPreserve(inner)
			for i := 0; i < len(parts) && i < 4; i++ {
				numStr := strings.TrimSpace(parts[i])
				if f, err := strconv.ParseFloat(numStr, 64); err == nil {
					if i%2 == 0 {
						parts[i] = floatToString(f*rx, precision)
					} else {
						parts[i] = floatToString(f*ry, precision)
					}
				}
			}
			prefix := m[:strings.Index(m, "(")]
			return prefix + "(" + strings.Join(parts, ",") + ")"
		})

		return content
	}

	// override block matcher
	reOverrideBlock := regexp.MustCompile(`\{([^}]*)\}`)

	// state for section detection
	section := ""
	inStyles := false
	inEvents := false
	var styleFormatFields []string
	var eventFormatFields []string

	var outLines []string

	// iterate lines, update styles / events / playres
	for _, rawLine := range lines {
		line := rawLine
		trim := strings.TrimSpace(line)

		// section headers
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			section = strings.ToLower(trim)
			inStyles = section == "[v4+ styles]" || section == "[v4+styles]" // tolerate slight variants
			inEvents = section == "[events]"
			outLines = append(outLines, rawLine)
			continue
		}

		// inside styles section
		if inStyles {
			// Format: line
			if m := regexp.MustCompile(`(?i)^\s*Format\s*:\s*(.+)$`).FindStringSubmatch(line); len(m) == 2 {
				// save format field names
				parts := strings.Split(m[1], ",")
				styleFormatFields = nil
				for _, p := range parts {
					styleFormatFields = append(styleFormatFields, strings.TrimSpace(p))
				}
				outLines = append(outLines, rawLine)
				continue
			}
			// Style: line
			if m := regexp.MustCompile(`(?i)^\s*Style\s*:\s*(.+)$`).FindStringSubmatch(line); len(m) == 2 {
				rawFields := splitCSVPreserve(m[1])
				fieldMap := map[string]string{}
				for i, name := range styleFormatFields {
					lname := strings.ToLower(name)
					val := ""
					if i < len(rawFields) {
						val = rawFields[i]
					}
					fieldMap[lname] = val
				}
				// apply transformations similar to Aegisub:
				// fontsize -> integer rounding style.fontsize = int(fontsize * ry + 0.5)
				if v, ok := fieldMap["fontsize"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						newFs := int(f*ry + 0.5)
						fieldMap["fontsize"] = strconv.Itoa(newFs)
					}
				}
				// outline / outline_w -> multiply by ry (Aegisub uses ry for these)
				for _, key := range []string{"outline", "outline_w", "shadow", "shadow_w"} {
					if v, ok := fieldMap[key]; ok && strings.TrimSpace(v) != "" {
						if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
							fieldMap[key] = floatToString(f*ry, precision)
						}
					}
				}
				// margins: marginl/marginr multiply rx, marginv multiply ry, integer rounding +0.5 and add state margin (we assume no extra global state margins here)
				if v, ok := fieldMap["marginl"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginl"] = strconv.Itoa(int(f*rx + 0.5))
					}
				}
				if v, ok := fieldMap["marginr"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginr"] = strconv.Itoa(int(f*rx + 0.5))
					}
				}
				if v, ok := fieldMap["marginv"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginv"] = strconv.Itoa(int(f*ry + 0.5))
					}
				}
				// scalex field: multiply by 'ar' (horizontal aspect)
				if v, ok := fieldMap["scalex"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["scalex"] = floatToString(f*ar, precision)
					}
				}

				// rebuild style line preserving order
				outFields := make([]string, len(styleFormatFields))
				for i, name := range styleFormatFields {
					lname := strings.ToLower(name)
					if val, ok := fieldMap[lname]; ok {
						outFields[i] = val
					} else if i < len(rawFields) {
						outFields[i] = rawFields[i]
					} else {
						outFields[i] = ""
					}
				}
				outLines = append(outLines, "Style: "+strings.Join(outFields, ","))
				continue
			}
			// otherwise copy
			outLines = append(outLines, rawLine)
			continue
		}

		// inside events section
		if inEvents {
			// Format:
			if m := regexp.MustCompile(`(?i)^\s*Format\s*:\s*(.+)$`).FindStringSubmatch(line); len(m) == 2 {
				parts := strings.Split(m[1], ",")
				eventFormatFields = nil
				for _, p := range parts {
					eventFormatFields = append(eventFormatFields, strings.TrimSpace(p))
				}
				outLines = append(outLines, rawLine)
				continue
			}
			// Dialogue:
			if m := regexp.MustCompile(`(?i)^\s*Dialogue\s*:\s*(.+)$`).FindStringSubmatch(line); len(m) == 2 {
				rawFields := splitCSVPreserve(m[1])
				fieldMap := map[string]string{}
				for i, name := range eventFormatFields {
					lname := strings.ToLower(name)
					val := ""
					if i < len(rawFields) {
						val = rawFields[i]
					}
					fieldMap[lname] = val
				}
				// margins: columns usually marginl/marginr/marginv -> integer rounding like styles
				if v, ok := fieldMap["marginl"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginl"] = strconv.Itoa(int(f*rx + 0.5))
					}
				}
				if v, ok := fieldMap["marginr"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginr"] = strconv.Itoa(int(f*rx + 0.5))
					}
				}
				if v, ok := fieldMap["marginv"]; ok && strings.TrimSpace(v) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						fieldMap["marginv"] = strconv.Itoa(int(f*ry + 0.5))
					}
				}
				// process text field overrides (identify text column)
				textIdx := -1
				for i, name := range eventFormatFields {
					if strings.ToLower(strings.TrimSpace(name)) == "text" {
						textIdx = i
						break
					}
				}
				if textIdx == -1 {
					textIdx = len(rawFields) - 1
				}
				text := ""
				if textIdx < len(rawFields) {
					text = rawFields[textIdx]
				}
				// process override blocks within text
				newText := reOverrideBlock.ReplaceAllStringFunc(text, func(m string) string {
					inner := m[1 : len(m)-1]
					processed := processOverrides(inner)
					// detect \pN (vector drawing mode) and if N>0 scale coordinates in same block
					rePmode := regexp.MustCompile(`(?i)\\p(\d+)`)
					if rp := rePmode.FindStringSubmatch(processed); len(rp) == 2 {
						if rp[1] != "0" {
							processed = scalePathCoords(processed, rx, ry)
						}
					}
					return "{" + processed + "}"
				})
				// place newText back
				if textIdx < len(rawFields) {
					rawFields[textIdx] = newText
				} else {
					rawFields = append(rawFields, newText)
				}
				// rebuild dialogue line using eventFormatFields if available
				if len(eventFormatFields) > 0 {
					outFields := make([]string, len(eventFormatFields))
					for i, name := range eventFormatFields {
						lname := strings.ToLower(strings.TrimSpace(name))
						if val, ok := fieldMap[lname]; ok && val != "" {
							outFields[i] = val
						} else {
							if i < len(rawFields) {
								outFields[i] = rawFields[i]
							} else {
								outFields[i] = ""
							}
						}
					}
					outLines = append(outLines, "Dialogue: "+strings.Join(outFields, ","))
				} else {
					outLines = append(outLines, "Dialogue: "+strings.Join(rawFields, ","))
				}
				continue
			}
			// otherwise copy events content
			outLines = append(outLines, rawLine)
			continue
		}

		// outside special sections: update PlayResX/Y
		if m := rePlayRes.FindStringSubmatch(line); len(m) == 3 {
			if strings.ToLower(m[1]) == "x" {
				outLines = append(outLines, fmt.Sprintf("PlayResX: %d", targetW))
			} else {
				outLines = append(outLines, fmt.Sprintf("PlayResY: %d", targetH))
			}
			continue
		}

		// default copy
		outLines = append(outLines, rawLine)
	}

	// join with CRLF and add BOM
	result := strings.Join(outLines, "\r\n")
	result = addBOMAndCRLF(result)
	return result
}

// addBOMAndCRLF ensures BOM present and CRLF line endings
func addBOMAndCRLF(s string) string {
	// normalize
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	// add BOM if missing
	if !strings.HasPrefix(s, "\uFEFF") {
		s = "\uFEFF" + s
	}
	return s
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <input.ass>")
		return
	}
	out := processASS(os.Args[1])
	// for convenience write file beside the input
	outPath := strings.TrimSuffix(os.Args[1], ".ass") + "_resampled_by_go.ass"
	ioutil.WriteFile(outPath, []byte(out), 0644)
	fmt.Println("Wrote:", outPath)
}
