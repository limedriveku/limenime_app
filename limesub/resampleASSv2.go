package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)
// processASS menghasilkan string ASS yang sudah di-resample menjadi 1920x1080,
// aspect mode = Stretch, dan mencoba meniru logika Aegisub C++ ResampleResolution
// sedekat mungkin (style scaling, dialogue override tags, drawing, margins, dll).

func processASS(path string) (string, error) {
	const destX = 1920.0
	const destY = 1080.0
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("gagal membaca file: %w", err)
	}	
	input := string(raw)
	
	if input == "" {
		return "", fmt.Errorf("input empty")
	}

	lines := strings.Split(input, "\n")

	// --- 1) baca PlayRes sumber (jika ada) ---
	var srcPlayX, srcPlayY float64
	foundPlayX := false
	foundPlayY := false

	for _, l := range lines {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "PlayResX") {
			parts := strings.SplitN(trim, ":", 2)
			if len(parts) == 2 {
				if v, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					srcPlayX = v
					foundPlayX = true
				}
			}
		}
		if strings.HasPrefix(trim, "PlayResY") {
			parts := strings.SplitN(trim, ":", 2)
			if len(parts) == 2 {
				if v, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					srcPlayY = v
					foundPlayY = true
				}
			}
		}
	}
	if srcPlayX == 0 || srcPlayY == 0 {
		// fallback sesuai diskusi
		srcPlayX = 1280
		srcPlayY = 720
	}

	// --- 2) Mode: Stretch (fixed) ---
	oldAR := srcPlayX / srcPlayY
	newAR := destX / destY
	horizontalStretch := newAR / oldAR // per Stretch mode in C++
	// margins provided to ResampleResolution: we'll default all zero (user can patch later)
	margin := [4]int{0, 0, 0, 0} // LEFT, RIGHT, TOP, BOTTOM

	// Add margins to original resolution (C++ adds margins before computing rx/ry)
	srcXWithMargins := srcPlayX + float64(margin[0]+margin[1])
	srcYWithMargins := srcPlayY + float64(margin[2]+margin[3])

	rx := destX / srcXWithMargins
	ry := destY / srcYWithMargins

	var rm float64
	if rx == ry {
		rm = rx
	} else {
		rm = math.Sqrt(rx * ry) // geometric mean like C++
	}

	ar := horizontalStretch // C++ uses horizontal_stretch stored in state->ar

	// --- helpers for transformations ---
	// transformDrawing == transform_drawing from C++:
	// takes string like "m 0 0 l 10 0 10 10" and shifts & scales numbers alternating x/y.
	transformDrawing := func(drawing string, shiftX, shiftY float64, scaleX, scaleY float64) string {
		parts := strings.Fields(drawing)
		var outParts []string
		isX := true
		for _, tok := range parts {
			// try parse number
			if v, err := strconv.ParseFloat(tok, 64); err == nil {
				if isX {
					v = (v + shiftX) * scaleX
				} else {
					v = (v + shiftY) * scaleY
				}
				// use fmt same-ish to C++ float_to_string: keep reasonable precision
				outParts = append(outParts, fmt.Sprintf("%.3f", v))
				isX = !isX
			} else if len(tok) == 1 {
				// single-letter command: m n l b s p c
				c := strings.ToLower(tok)
				if strings.Contains("mnlbspc", c) { // using 'n' as in original code
					isX = true
					outParts = append(outParts, c)
				} else {
					// unknown token - keep as is
					outParts = append(outParts, tok)
				}
			} else {
				// not a single-letter token and not a number - keep
				outParts = append(outParts, tok)
			}
		}
		return strings.Join(outParts, " ")
	}

	// regexes used for tag matching inside override blocks
	reOverrideBlock := regexp.MustCompile(`\{[^}]*\}`) // match { ... } blocks
	// common tag patterns (capture numbers)
	rePos := regexp.MustCompile(`\\pos\(([-\d.]+),\s*([-\d.]+)\)`)
	reMove := regexp.MustCompile(`\\move\(([-\d.]+),\s*([-\d.]+),\s*([-\d.]+),\s*([-\d.]+)(?:,[-\d.]+,[-\d.]+)?\)`)
	reOrg := regexp.MustCompile(`\\org\(([-\d.]+),\s*([-\d.]+)\)`)
	reClipRect := regexp.MustCompile(`\\(i?clip)\(([-\d.]+),\s*([-\d.]+),\s*([-\d.]+),\s*([-\d.]+)\)`)
	reClipVec := regexp.MustCompile(`\\(i?clip)\((m[^\)]+)\)`) // captures type and "m ..."

	// single-value tags
	reFS := regexp.MustCompile(`\\fs([-\d.]+)`)
	reBORD := regexp.MustCompile(`\\bord([-\d.]+)`)
	reXBORD := regexp.MustCompile(`\\xbord([-\d.]+)`)
	reYBORD := regexp.MustCompile(`\\ybord([-\d.]+)`)
	reSHAD := regexp.MustCompile(`\\shad([-\d.]+)`)
	reXSHAD := regexp.MustCompile(`\\xshad([-\d.]+)`)
	reYSHAD := regexp.MustCompile(`\\yshad([-\d.]+)`)
	reFSP := regexp.MustCompile(`\\fsp([-\d.]+)`)
	reFSCX := regexp.MustCompile(`\\fscx([-\d.]+)`)
	reFSCY := regexp.MustCompile(`\\fscy([-\d.]+)`)
	// other absolute/relative tags often seen: \fax, \fay (we follow C++ mapping: treat as RELATIVE or leave)
	reFAX := regexp.MustCompile(`\\fax\(([-\d.]+)\)`)
	reFAY := regexp.MustCompile(`\\fay\(([-\d.]+)\)`)

	// helper to replace numbers alternating x/y with shift offsets and axis scales
	replaceCoordsWithShift := func(s string, nums []string, shiftX, shiftY float64, sx, sy float64) string {
		out := s
		for i := 0; i < len(nums); i++ {
			v, _ := strconv.ParseFloat(nums[i], 64)
			if i%2 == 0 {
				newv := (v + shiftX) * sx
				out = strings.Replace(out, nums[i], fmt.Sprintf("%.3f", newv), 1)
			} else {
				newv := (v + shiftY) * sy
				out = strings.Replace(out, nums[i], fmt.Sprintf("%.3f", newv), 1)
			}
		}
		return out
	}

	// whether we saw PlayRes fields in file
	needInsertPlayRes := !(foundPlayX && foundPlayY)

	inStyles := false
	inEvents := false
	var outLines []string

	for _, raw := range lines {
		line := raw
		trim := strings.TrimSpace(line)

		// detect sections and insert PlayRes before [V4+ Styles] if needed
		if trim == "[V4+ Styles]" && needInsertPlayRes {
			outLines = append(outLines, "PlayResX: 1920")
			outLines = append(outLines, "PlayResY: 1080")
			needInsertPlayRes = false
		}
		if trim == "[V4+ Styles]" {
			inStyles = true
			inEvents = false
			outLines = append(outLines, line)
			continue
		}
		if trim == "[Events]" {
			inEvents = true
			inStyles = false
			outLines = append(outLines, line)
			continue
		}

		// Ensure header PlayRes values in existing lines are set to dest values
		if strings.HasPrefix(trim, "PlayResX") {
			outLines = append(outLines, "PlayResX: 1920")
			continue
		}
		if strings.HasPrefix(trim, "PlayResY") {
			outLines = append(outLines, "PlayResY: 1080")
			continue
		}

		// ----- Styles processing (similar to resample_style) -----
		if inStyles && strings.HasPrefix(strings.TrimSpace(line), "Style:") {
			// naive split by comma (ASS style fields normally don't contain commas)
			parts := strings.Split(line, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			if len(parts) >= 23 {
				// indexes similar to C++ expectations:
				// 0: "Style: Name"
				// 2: FontSize
				// 11: ScaleX
				// 12: ScaleY
				// 13: Spacing
				// 16: Outline
				// 17: Shadow
				// 19: MarginL
				// 20: MarginR
				// 21: MarginV
				// Parse existing values
				fontSize, _ := strconv.ParseFloat(parts[2], 64)
				scaleXVal, _ := strconv.ParseFloat(parts[11], 64)
				scaleYVal, _ := strconv.ParseFloat(parts[12], 64)
				spacing, _ := strconv.ParseFloat(parts[13], 64)
				outline, _ := strconv.ParseFloat(parts[16], 64)
				shadow, _ := strconv.ParseFloat(parts[17], 64)
				marginL, _ := strconv.ParseFloat(parts[19], 64)
				marginR, _ := strconv.ParseFloat(parts[20], 64)
				marginV, _ := strconv.ParseFloat(parts[21], 64)

				// Apply C++-like scaling:
				// fontsize = int(fontsize * ry + 0.5)
				newFs := int(fontSize*ry + 0.5)
				parts[2] = strconv.Itoa(newFs)

				// style.scalex *= ar  (ScaleX is percent-like in ASS; C++ uses style.scalex *= state->ar)
				parts[11] = fmt.Sprintf("%.3f", scaleXVal*ar)
				// style.scaley left as-is (C++ multiplies style.scalex by ar, doesn't change scaley)
				parts[12] = fmt.Sprintf("%.3f", scaleYVal)

				// spacing, outline, shadow scaled by ry
				parts[13] = fmt.Sprintf("%.3f", spacing*ry)
				parts[16] = fmt.Sprintf("%.3f", outline*ry)
				parts[17] = fmt.Sprintf("%.3f", shadow*ry)

				// margins: (margin + global_margin) * axis, rounded +0.5
				newML := int((marginL+float64(margin[0]))*rx + 0.5)
				newMR := int((marginR+float64(margin[1]))*rx + 0.5)
				newMV := int((marginV+float64(margin[2]))*ry + 0.5)
				parts[19] = strconv.Itoa(newML)
				parts[20] = strconv.Itoa(newMR)
				parts[21] = strconv.Itoa(newMV)

				line = strings.Join(parts, ",")
			}
			outLines = append(outLines, line)
			continue
		}

		// ----- Events / Dialogue processing -----
		if inEvents && strings.HasPrefix(strings.TrimSpace(line), "Dialogue:") {
			// We need to split the Dialogue header and text reliably.
			// Format: Dialogue: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text
			// We'll split into first 9 commas so that Text (which may contain commas) stays as last element.
			after := strings.TrimSpace(strings.TrimPrefix(line, "Dialogue:"))
			parts := strings.SplitN(after, ",", 9)
			if len(parts) < 9 {
				// malformed, pass through unchanged
				outLines = append(outLines, line)
				continue
			}

			// Extract margin ints
			marginL, _ := strconv.Atoi(strings.TrimSpace(parts[5]))
			marginR, _ := strconv.Atoi(strings.TrimSpace(parts[6]))
			marginVVal, _ := strconv.Atoi(strings.TrimSpace(parts[7]))
			// Update margins per-dialog like C++
			newMarginL := int((float64(marginL)+float64(margin[0]))*rx + 0.5)
			newMarginR := int((float64(marginR)+float64(margin[1]))*rx + 0.5)
			newMarginV := int((float64(marginVVal)+float64(margin[2]))*ry + 0.5)
			parts[5] = strconv.Itoa(newMarginL)
			parts[6] = strconv.Itoa(newMarginR)
			parts[7] = strconv.Itoa(newMarginV)

			// Effect + Text: parts[8] contains "Effect,Text..."
			effectAndText := parts[8]
			idx := strings.Index(effectAndText, ",")
			effect := ""
			text := ""
			if idx >= 0 {
				effect = effectAndText[:idx]
				text = effectAndText[idx+1:]
			} else {
				effect = effectAndText
				text = ""
			}

			// We must process override blocks inside text { ... }
			processedText := text

			// Find all override blocks { ... } and process tags inside each block
			processedText = reOverrideBlock.ReplaceAllStringFunc(processedText, func(block string) string {
				// block includes braces { ... }
				inside := block[1 : len(block)-1] // remove braces
				// 1) Process positional tags: \pos, \org, \move
				inside = rePos.ReplaceAllStringFunc(inside, func(m string) string {
					sub := rePos.FindStringSubmatch(m)
					if len(sub) < 3 {
						return m
					}
					// ABSOLUTE_POS_X uses rx and shift margin LEFT, ABSOLUTE_POS_Y uses ry and shift TOP
					x, _ := strconv.ParseFloat(sub[1], 64)
					y, _ := strconv.ParseFloat(sub[2], 64)
					newx := (x + float64(margin[0])) * rx
					newy := (y + float64(margin[2])) * ry
					return fmt.Sprintf(`\pos(%.3f,%.3f)`, newx, newy)
				})
				inside = reOrg.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reOrg.FindStringSubmatch(m)
					if len(sub) < 3 {
						return m
					}
					x, _ := strconv.ParseFloat(sub[1], 64)
					y, _ := strconv.ParseFloat(sub[2], 64)
					newx := (x + float64(margin[0])) * rx
					newy := (y + float64(margin[2])) * ry
					return fmt.Sprintf(`\org(%.3f,%.3f)`, newx, newy)
				})
				inside = reMove.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reMove.FindStringSubmatch(m)
					if len(sub) < 5 {
						return m
					}
					// scale x1,y1,x2,y2 with margin offsets
					x1, _ := strconv.ParseFloat(sub[1], 64)
					y1, _ := strconv.ParseFloat(sub[2], 64)
					x2, _ := strconv.ParseFloat(sub[3], 64)
					y2, _ := strconv.ParseFloat(sub[4], 64)
					newx1 := (x1 + float64(margin[0])) * rx
					newy1 := (y1 + float64(margin[2])) * ry
					newx2 := (x2 + float64(margin[0])) * rx
					newy2 := (y2 + float64(margin[2])) * ry
					// preserve any trailing ",t1,t2" by finding closing ) in original and appending.
					// Simpler: replace first 4 coords only.
					// Build replacement prefix
					return fmt.Sprintf(`\move(%.3f,%.3f,%.3f,%.3f)`, newx1, newy1, newx2, newy2)
				})

				// 2) Single-value overrides mapped by classification
				// ABSOLUTE_SIZE_X -> multiply by rx ? (C++ uses classifications: ABSOLUTE_SIZE_X uses rx)
				// In C++ mapping: ABSOLUTE_SIZE_X => state->rx, ABSOLUTE_SIZE_Y => state->ry, ABSOLUTE_SIZE_XY => rm
				// Map common tags:
				// \fs (font size) -> ABSOLUTE_SIZE_Y (C++ multiplies fontsize by ry and rounds to int)
				inside = reFS.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reFS.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					// C++: style.fontsize = int(style.fontsize * state->ry + 0.5)
					newv := v * ry
					// in C++ dialog-level override \fs is float processed by ProcessParameters:
					// it sets float -> cur->Set((cur->Get<double>() + shift) * resizer)
					// We'll format with 3 decimals to be safe
					return fmt.Sprintf(`\fs%.3f`, newv)
				})

				// \bord -> ABSOLUTE_SIZE_XY? In C++ they treat Outline and Shadow multipled by state->ry in style,
				// but for override tags classification likely ABSOLUTE_SIZE_XY or ABSOLUTE_SIZE? Best-effort:
				inside = reBORD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reBORD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					// C++ dialogue ProcessParameters uses classification: ABSOLUTE_SIZE_XY => resizer = state->rm
					newv := v * rm
					return fmt.Sprintf(`\bord%.3f`, newv)
				})
				inside = reXBORD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reXBORD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * rx
					return fmt.Sprintf(`\xbord%.3f`, newv)
				})
				inside = reYBORD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reYBORD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * ry
					return fmt.Sprintf(`\ybord%.3f`, newv)
				})

				inside = reSHAD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reSHAD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * rm // C++ style.shadow_w *= state->ry, but ProcessParameters may use rm for XY; best to use rm
					// To match C++ more closely, use ry for shadow (style) but rm for overrides — we use rm to reflect ABSOLUTE_SIZE_XY mapping
					return fmt.Sprintf(`\shad%.3f`, newv)
				})
				inside = reXSHAD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reXSHAD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * rx
					return fmt.Sprintf(`\xshad%.3f`, newv)
				})
				inside = reYSHAD.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reYSHAD.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * ry
					return fmt.Sprintf(`\yshad%.3f`, newv)
				})

				// \fsp spacing -> treat as absolute size Y in C++ style spacing = spacing * ry
				inside = reFSP.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reFSP.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * ry
					return fmt.Sprintf(`\fsp%.3f`, newv)
				})

				// \fscx: RELATIVE_SIZE_X -> multiply by ar (C++ uses state->ar)
				inside = reFSCX.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reFSCX.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * ar
					return fmt.Sprintf(`\fscx%.3f`, newv)
				})
				inside = reFSCY.ReplaceAllStringFunc(inside, func(m string) string {
					return m
				})
				// \fscy: RELATIVE_SIZE_Y -> C++ handler does nothing for RELATIVE_SIZE_Y in code shown; leave unchanged.
				// We'll still capture and leave as-is (i.e. no scaling)

				// \fax / \fay : C++ treats some relative tags specially; we'll multiply by rm minimally (best-effort)
				inside = reFAX.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reFAX.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * rm
					return fmt.Sprintf(`\\fax(%.3f)`, newv)
				})
				inside = reFAY.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reFAY.FindStringSubmatch(m)
					if len(sub) < 2 {
						return m
					}
					v, _ := strconv.ParseFloat(sub[1], 64)
					newv := v * rm
					return fmt.Sprintf(`\\fay(%.3f)`, newv)
				})

				// 3) rectangular clip: add margin offsets then scale (ABSOLUTE_POS/ABSOLUTE_SIZE)
				inside = reClipRect.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reClipRect.FindStringSubmatch(m)
					if len(sub) < 6 {
						return m
					}
					// sub[1] = clip or iclip
					coords := []string{sub[2], sub[3], sub[4], sub[5]}
					return replaceCoordsWithShift(m, coords, float64(margin[0]), float64(margin[2]), rx, ry)
				})

				// 4) vector clip: \clip(m ...)
				inside = reClipVec.ReplaceAllStringFunc(inside, func(m string) string {
					sub := reClipVec.FindStringSubmatch(m)
					if len(sub) < 3 {
						return m
					}
					clipType := sub[1]     // "clip" or "iclip"
					coordsStr := sub[2]    // e.g. "m 0 0 l 10 0 10 10"
					// extract numbers
					nums := regexp.MustCompile(`-?[\d.]+`).FindAllString(coordsStr, -1)
					if len(nums) == 0 {
						// nothing to change
						return m
					}
					scaled := make([]string, len(nums))
					for i, n := range nums {
						v, _ := strconv.ParseFloat(n, 64)
						if i%2 == 0 {
							// X
							scaled[i] = fmt.Sprintf("%.3f", (v+float64(margin[0]))*rx)
						} else {
							// Y
							scaled[i] = fmt.Sprintf("%.3f", (v+float64(margin[2]))*ry)
						}
					}
					outCoords := coordsStr
					for i := 0; i < len(nums); i++ {
						outCoords = strings.Replace(outCoords, nums[i], scaled[i], 1)
					}
					return fmt.Sprintf(`\%s(%s)`, clipType, outCoords)
				})

				// 5) drawing commands inside plain text (not clip), e.g. {\p1} drawing in text blocks
				// There can be drawing sequences after \pN or in stand-alone drawing events.
				// We'll scale occurrences of patterns starting with an 'm ' or sequences of commands/numbers.
				// Attempt: transform any "m " sequences inside the override block.
				// Find "m " followed by content up to maybe next \ or end
				reDrawSeq := regexp.MustCompile(`(?i)m(?:[^\}\\]+)`) // crude but catches "m 0 0 l 10 0 10 10"
				inside = reDrawSeq.ReplaceAllStringFunc(inside, func(mdraw string) string {
					// remove leading/trailing spaces
					md := strings.TrimSpace(mdraw)
					// apply transformDrawing with margin offsets and rx/ry
					return transformDrawing(md, float64(margin[0]), float64(margin[2]), rx, ry)
				})

				// finished processing inside block
				return "{" + inside + "}"
			})

			// Rebuild Dialogue line
			head := strings.Join(parts[0:8], ",")
			newLine := "Dialogue: " + head + "," + effect + "," + processedText
			outLines = append(outLines, newLine)
			continue
		}

		// default: pass through line unchanged
		outLines = append(outLines, line)
	}

	// If PlayRes not present anywhere, ensure they are at top
	if needInsertPlayRes {
		out := "PlayResX: 1920\nPlayResY: 1080\n" + strings.Join(outLines, "\n")
		return out, nil
	}

	return limenimizerASS(strings.Join(outLines, "\n")), nil
}

func limenimizerASS(input string) string {
	lines := strings.Split(input, "\n")
	var output []string
	inStyleSection := false
	inEventSection := false
	var styleLines []string

	for _, line := range lines {
		trim := strings.TrimSpace(line)

		// Deteksi section
		if strings.HasPrefix(trim, "[V4+ Styles]") {
			inStyleSection = true
			inEventSection = false
			output = append(output, line)
			continue
		}

		if strings.HasPrefix(trim, "[Events]") {
			// Sebelum masuk ke Events, tambahkan style baru di akhir Style section
			if len(styleLines) > 0 {
				styleLines = append(styleLines,
					"Style: res,Basic Comical NC,1080,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,0,0,0,0,1,2,2,2,10,10,10,1")
				output = append(output, styleLines...)
				styleLines = nil
			}
			inEventSection = true
			inStyleSection = false
			output = append(output, line)
			continue
		}

		// Proses Style section
		if inStyleSection {
			if strings.HasPrefix(trim, "Style:") {
				// Ubah font ke Basic Comical NC
				parts := strings.SplitN(line, ",", 3)
				if len(parts) >= 3 {
					styleParts := strings.SplitN(line, ",", 3)
					styleParts[1] = "Basic Comical NC"
					line = strings.Join(styleParts, ",")
				}
				styleLines = append(styleLines, line)
				continue
			} else {
				// Baris non-Style tapi masih dalam Style section
				styleLines = append(styleLines, line)
				continue
			}
		}

		// Proses Event section
		if inEventSection && strings.HasPrefix(trim, "Dialogue:") {
			re := regexp.MustCompile(`\\fn[^\\}]*`)
			if re.MatchString(line) {
				line = re.ReplaceAllString(line, `\fnBasic Comical NC`)
			}
			output = append(output, line)
			continue
		}

		output = append(output, line)
	}

	// Jika file tidak punya [Events] (jarang, tapi jaga-jaga)
	if len(styleLines) > 0 {
		styleLines = append(styleLines,
			"Style: res,Basic Comical NC,1080,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,0,0,0,0,1,2,2,2,10,10,10,1")
		output = append(output, styleLines...)
	}

	return strings.Join(output, "\n")
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
