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

// ProcessASS rescales ASS content to 1920x1080 following the strict spec and fixes:
// - dynamic Format: mapping for Styles/Events
// - vector clip detection (scale param preserved) and proper scaling of path coords
// - drawing \p tokenization and pair-scaling
// - float trimming and integer rounding
// - plevel tracking
// Returns "" if PlayResX/PlayResY cannot be parsed.
func ProcessASS(path string) string {
	const targetW = 1920.0
	const targetH = 1080.0
  
  //Reading filepath
  raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("gagal membaca file: %w", err)
	}	
	assContent := string(raw)

	if assContent == "" {
		return ""
	}

	// normalize newlines
	content := strings.ReplaceAll(assContent, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	// 1) find PlayResX / PlayResY
	var sourceW, sourceH float64
	for _, ln := range lines {
		l := strings.TrimSpace(ln)
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "playresx") {
			parts := strings.SplitN(l, ":", 2)
			if len(parts) < 2 {
				parts = strings.SplitN(l, "=", 2)
			}
			if len(parts) >= 2 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					sourceW = f
				}
			}
		}
		if strings.HasPrefix(low, "playresy") {
			parts := strings.SplitN(l, ":", 2)
			if len(parts) < 2 {
				parts = strings.SplitN(l, "=", 2)
			}
			if len(parts) >= 2 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					sourceH = f
				}
			}
		}
	}
	if sourceW == 0 || sourceH == 0 {
		return ""
	}

	// scaling factors
	RX := targetW / sourceW
	RY := targetH / sourceH
	RM := math.Sqrt(RX * RY)
	AR := (targetW/targetH) / (sourceW/sourceH)
	almostOne := func(x float64) bool { return math.Abs(x-1.0) <= 1e-9 }

	// helpers for formatting
	trimFloat := func(v float64) string {
		// up to 6 decimals, trim trailing zeros & dot
		s := strconv.FormatFloat(v, 'f', 6, 64)
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
		if s == "" {
			return "0"
		}
		return s
	}
	intRound := func(v float64) string {
		return strconv.Itoa(int(math.Floor(v + 0.5)))
	}

	// regexes
	reOverrideBlock := regexp.MustCompile(`\{[^}]*\}`)
	numberRe := regexp.MustCompile(`[+-]?\d*\.?\d+`)

	// permissive tag regexes
	rePos := regexp.MustCompile(`(?i)\\pos\s*\(\s*([+-]?\d*\.?\d+)\s*,\s*([+-]?\d*\.?\d+)\s*\)`)
	reMove := regexp.MustCompile(`(?i)\\move\s*\(\s*([+-]?\d*\.?\d+)\s*,\s*([+-]?\d*\.?\d+)\s*,\s*([+-]?\d*\.?\d+)\s*,\s*([+-]?\d*\.?\d+)(\s*,\s*[^)]*)?\)`)
	reOrg := regexp.MustCompile(`(?i)\\org\s*\(\s*([+-]?\d*\.?\d+)\s*,\s*([+-]?\d*\.?\d+)\s*\)`)
	reIClip := regexp.MustCompile(`(?i)\\iclip\s*\(\s*([^\)]*)\)`)
	reClip := regexp.MustCompile(`(?i)\\clip\s*\(\s*([^\)]*)\)`)
	reFscx := regexp.MustCompile(`(?i)\\fscx\s*([+-]?\d*\.?\d+)`)
	reFscy := regexp.MustCompile(`(?i)\\fscy\s*([+-]?\d*\.?\d+)`)
	reFs := regexp.MustCompile(`(?i)\\fs\s*([+-]?\d*\.?\d+)`)
	reFsp := regexp.MustCompile(`(?i)\\fsp\s*([+-]?\d*\.?\d+)`)
	reBord := regexp.MustCompile(`(?i)\\bord\s*([+-]?\d*\.?\d+)`)
	reShad := regexp.MustCompile(`(?i)\\shad\s*([+-]?\d*\.?\d+)`)
	reXBord := regexp.MustCompile(`(?i)\\xbord\s*([+-]?\d*\.?\d+)`)
	reYBord := regexp.MustCompile(`(?i)\\ybord\s*([+-]?\d*\.?\d+)`)
	reXShad := regexp.MustCompile(`(?i)\\xshad\s*([+-]?\d*\.?\d+)`)
	reYShad := regexp.MustCompile(`(?i)\\yshad\s*([+-]?\d*\.?\d+)`)
	rePbo := regexp.MustCompile(`(?i)\\pbo\s*([+-]?\d*\.?\d+)`)
	rePlevel := regexp.MustCompile(`(?i)\\p\s*([0-9]+)`)

	// utility: split CSV into exactly n parts (first n-1 commas as separators)
	splitByCommasN := func(s string, n int) []string {
		if n <= 1 {
			return []string{s}
		}
		res := make([]string, 0, n)
		start := 0
		count := 0
		for i := 0; i < len(s) && count < n-1; i++ {
			if s[i] == ',' {
				res = append(res, strings.TrimSpace(s[start:i]))
				start = i + 1
				count++
			}
		}
		if start <= len(s) {
			res = append(res, strings.TrimSpace(s[start:]))
		}
		if len(res) < n {
			parts := strings.Split(s, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			return parts
		}
		return res
	}

	// scale numeric list alternating x,y
	scaleXYAlternating := func(s string, preferIntX, preferIntY bool) string {
		nums := numberRe.FindAllString(s, -1)
		if len(nums) == 0 {
			return s
		}
		out := s
		idx := 0
		for _, nm := range nums {
			fv, err := strconv.ParseFloat(nm, 64)
			if err != nil {
				continue
			}
			var rep string
			if idx%2 == 0 {
				sc := fv * RX
				if preferIntX {
					rep = intRound(sc)
				} else {
					rep = trimFloat(sc)
				}
			} else {
				sc := fv * RY
				if preferIntY {
					rep = intRound(sc)
				} else {
					rep = trimFloat(sc)
				}
			}
			out = strings.Replace(out, nm, rep, 1)
			idx++
		}
		return out
	}

	// parse drawing path tokens (for \p and vector clip)
	// Tokenizer: commands are letters (m,l,b,n,s,c,q...), numbers are numeric tokens.
	tokenizePath := func(s string) []string {
		// insert spaces around letters to ease splitting (but keep numbers intact)
		// we want tokens like: "m", "150", "150", "l", "300", "300", ...
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			ch := s[i]
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				// ensure space before and after command if needed
				if b.Len() > 0 {
					last := b.String()
					if !strings.HasSuffix(last, " ") {
						b.WriteByte(' ')
					}
				}
				b.WriteByte(ch)
				// add space after
				if i+1 < len(s) && s[i+1] != ' ' {
					b.WriteByte(' ')
				}
			} else {
				b.WriteByte(ch)
			}
		}
		// collapse multiple spaces
		toks := strings.Fields(b.String())
		return toks
	}

	// scale path tokens by alternating x/y for numeric tokens; preserve commands
	scalePathTokens := func(tokens []string) string {
		outTokens := make([]string, 0, len(tokens))
		xyIdx := 0 // counts numeric tokens only to alternate x/y
		for _, tok := range tokens {
			// detect number
			if numberRe.MatchString(tok) {
				// parse number
				if fv, err := strconv.ParseFloat(tok, 64); err == nil {
					if xyIdx%2 == 0 {
						outTokens = append(outTokens, trimFloat(fv*RX))
					} else {
						outTokens = append(outTokens, trimFloat(fv*RY))
					}
					xyIdx++
					continue
				}
			}
			// otherwise command or other token, copy as-is
			outTokens = append(outTokens, tok)
		}
		return strings.Join(outTokens, " ")
	}

	// process override block content; returns processed content and plevel
	processOverrideContent := func(content string) (string, int) {
		plevel := 0
		orig := content

		// 1) pos/move/org
		content = rePos.ReplaceAllStringFunc(content, func(m string) string {
			sub := rePos.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			x, _ := strconv.ParseFloat(sub[1], 64)
			y, _ := strconv.ParseFloat(sub[2], 64)
			return `\pos(` + trimFloat(x*RX) + `,` + trimFloat(y*RY) + `)`
		})
		content = reMove.ReplaceAllStringFunc(content, func(m string) string {
			sub := reMove.FindStringSubmatch(m)
			if len(sub) < 5 {
				return m
			}
			x1, _ := strconv.ParseFloat(sub[1], 64)
			y1, _ := strconv.ParseFloat(sub[2], 64)
			x2, _ := strconv.ParseFloat(sub[3], 64)
			y2, _ := strconv.ParseFloat(sub[4], 64)
			rest := ""
			if len(sub) >= 6 {
				rest = strings.TrimSpace(sub[5])
			}
			if rest != "" {
				return `\move(` + trimFloat(x1*RX) + `,` + trimFloat(y1*RY) + `,` + trimFloat(x2*RX) + `,` + trimFloat(y2*RY) + `,` + strings.TrimLeft(rest, ",") + `)`
			}
			return `\move(` + trimFloat(x1*RX) + `,` + trimFloat(y1*RY) + `,` + trimFloat(x2*RX) + `,` + trimFloat(y2*RY) + `)`
		})
		content = reOrg.ReplaceAllStringFunc(content, func(m string) string {
			sub := reOrg.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			x, _ := strconv.ParseFloat(sub[1], 64)
			y, _ := strconv.ParseFloat(sub[2], 64)
			return `\org(` + trimFloat(x*RX) + `,` + trimFloat(y*RY) + `)`
		})

		// 2) clip / iclip
		// need to detect vector clip: contains letter commands after optional scale param
		content = reClip.ReplaceAllStringFunc(content, func(m string) string {
			sub := reClip.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			inside := strings.TrimSpace(sub[1])
			// detect if vector: presence of letters like m/l/b/n/s etc.
			if regexp.MustCompile(`[A-Za-z]`).MatchString(inside) {
				// vector clip. Common forms:
				//  - "<scale>,<rest commands...>"
				//  - sometimes no comma between scale and rest? ASS typically uses comma.
				// We'll split by first comma: first token likely scale.
				parts := splitByCommasN(inside, 2)
				if len(parts) >= 2 {
					scaleTok := strings.TrimSpace(parts[0])
					restTok := strings.TrimSpace(parts[1])
					// scaleTok is preserved (do NOT scale)
					// restTok contains path commands; tokenize and scale coordinates
					toks := tokenizePath(restTok)
					scaled := scalePathTokens(toks)
					return `\clip(` + scaleTok + `, ` + scaled + `)`
				}
				// fallback: if no comma, maybe starts with 'm', then scale all alternating
				toks := tokenizePath(inside)
				scaled := scalePathTokens(toks)
				return `\clip(` + scaled + `)`
			}
			// else assume numeric rectangle x1,y1,x2,y2 (or more)
			parts := splitByCommasN(inside, 4)
			if len(parts) >= 4 {
				x1, _ := strconv.ParseFloat(parts[0], 64)
				y1, _ := strconv.ParseFloat(parts[1], 64)
				x2, _ := strconv.ParseFloat(parts[2], 64)
				y2, _ := strconv.ParseFloat(parts[3], 64)
				return `\clip(` + trimFloat(x1*RX) + `,` + trimFloat(y1*RY) + `,` + trimFloat(x2*RX) + `,` + trimFloat(y2*RY) + `)`
			}
			// fallback generic alternating scale
			return `\clip(` + scaleXYAlternating(inside, false, false) + `)`
		})

		content = reIClip.ReplaceAllStringFunc(content, func(m string) string {
			sub := reIClip.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			inside := strings.TrimSpace(sub[1])
			if regexp.MustCompile(`[A-Za-z]`).MatchString(inside) {
				parts := splitByCommasN(inside, 2)
				if len(parts) >= 2 {
					scaleTok := strings.TrimSpace(parts[0])
					restTok := strings.TrimSpace(parts[1])
					toks := tokenizePath(restTok)
					scaled := scalePathTokens(toks)
					return `\iclip(` + scaleTok + `, ` + scaled + `)`
				}
				toks := tokenizePath(inside)
				return `\iclip(` + scalePathTokens(toks) + `)`
			}
			parts := splitByCommasN(inside, 4)
			if len(parts) >= 4 {
				x1, _ := strconv.ParseFloat(parts[0], 64)
				y1, _ := strconv.ParseFloat(parts[1], 64)
				x2, _ := strconv.ParseFloat(parts[2], 64)
				y2, _ := strconv.ParseFloat(parts[3], 64)
				return `\iclip(` + trimFloat(x1*RX) + `,` + trimFloat(y1*RY) + `,` + trimFloat(x2*RX) + `,` + trimFloat(y2*RY) + `)`
			}
			return `\iclip(` + scaleXYAlternating(inside, false, false) + `)`
		})

		// 3) font & spacing & sizes
		content = reFscx.ReplaceAllStringFunc(content, func(m string) string {
			sub := reFscx.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\fscx` + trimFloat(v*RX)
		})
		content = reFscy.ReplaceAllStringFunc(content, func(m string) string {
			sub := reFscy.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\fscy` + trimFloat(v*RY)
		})
		content = reFs.ReplaceAllStringFunc(content, func(m string) string {
			sub := reFs.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\fs` + intRound(v*RY)
		})
		// per spec fsp -> RY
		content = reFsp.ReplaceAllStringFunc(content, func(m string) string {
			sub := reFsp.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\fsp` + trimFloat(v*RY)
		})

		// 4) borders & shadows (override)
		content = reBord.ReplaceAllStringFunc(content, func(m string) string {
			sub := reBord.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\bord` + trimFloat(v*RM)
		})
		content = reShad.ReplaceAllStringFunc(content, func(m string) string {
			sub := reShad.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\shad` + trimFloat(v*RM)
		})
		content = reXBord.ReplaceAllStringFunc(content, func(m string) string {
			sub := reXBord.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\xbord` + trimFloat(v*RX)
		})
		content = reYBord.ReplaceAllStringFunc(content, func(m string) string {
			sub := reYBord.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\ybord` + trimFloat(v*RY)
		})
		content = reXShad.ReplaceAllStringFunc(content, func(m string) string {
			sub := reXShad.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\xshad` + trimFloat(v*RX)
		})
		content = reYShad.ReplaceAllStringFunc(content, func(m string) string {
			sub := reYShad.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\yshad` + trimFloat(v*RY)
		})
		content = rePbo.ReplaceAllStringFunc(content, func(m string) string {
			sub := rePbo.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			v, _ := strconv.ParseFloat(sub[1], 64)
			return `\pbo` + trimFloat(v*RY)
		})

		// 5) plevel detection
		pl := rePlevel.FindStringSubmatch(content)
		if len(pl) >= 2 {
			if n, err := strconv.Atoi(pl[1]); err == nil {
				plevel = n
			}
		}

		_ = orig
		return content, plevel
	}

	// process event text: handle override blocks and drawing plevel
	processEventText := func(text string) string {
		if text == "" {
			return text
		}
		matches := reOverrideBlock.FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			return text
		}
		out := strings.Builder{}
		last := 0
		plevel := 0
		for _, idx := range matches {
			start, end := idx[0], idx[1]
			// plain before override
			if start > last {
				plain := text[last:start]
				if plevel > 0 {
					plain = scaleXYAlternating(plain, false, false)
				}
				out.WriteString(plain)
			}
			ov := text[start:end]
			inner := ov[1 : len(ov)-1]
			processed, newP := processOverrideContent(inner)
			plevel = newP
			out.WriteString("{" + processed + "}")
			last = end
		}
		// trailing plain
		if last < len(text) {
			tr := text[last:]
			if plevel > 0 {
				tr = scaleXYAlternating(tr, false, false)
			}
			out.WriteString(tr)
		}
		return out.String()
	}

	// MAIN PASS: iterate lines and process sections with dynamic Format mapping
	outLines := make([]string, 0, len(lines))
	section := ""
	inStyles := false
	inEvents := false
	var stylesFormat []string
	var eventsFormat []string

	for _, raw := range lines {
		line := raw
		trim := strings.TrimSpace(line)
		// detect section header
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			section = strings.ToLower(strings.Trim(trim, "[]"))
			inStyles = (section == "v4+ styles" || section == "v4 styles" || section == "styles")
			inEvents = (section == "events")
			outLines = append(outLines, line)
			continue
		}

		// Script Info: update PlayResX/PlayResY
		if section == "script info" {
			lower := strings.ToLower(strings.TrimSpace(line))
			if strings.HasPrefix(lower, "playresx") {
				outLines = append(outLines, "PlayResX: "+intRound(targetW))
				continue
			}
			if strings.HasPrefix(lower, "playresy") {
				outLines = append(outLines, "PlayResY: "+intRound(targetH))
				continue
			}
			outLines = append(outLines, line)
			continue
		}

		// Styles section
		if inStyles {
			lower := strings.ToLower(strings.TrimSpace(line))
			if strings.HasPrefix(lower, "format:") {
				// preserve format line exactly as original
				// also parse field names
				idx := strings.Index(line, ":")
				fields := strings.Split(line[idx+1:], ",")
				for i := range fields {
					fields[i] = strings.TrimSpace(fields[i])
				}
				stylesFormat = fields
				outLines = append(outLines, line)
				continue
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "style:") {
				// if we lack format mapping, preserve
				if len(stylesFormat) == 0 {
					outLines = append(outLines, line)
					continue
				}
				idx := strings.Index(line, ":")
				if idx < 0 {
					outLines = append(outLines, line)
					continue
				}
				rest := strings.TrimSpace(line[idx+1:])
				parts := splitByCommasN(rest, len(stylesFormat))
				if len(parts) != len(stylesFormat) {
					// cannot parse reliably: preserve
					outLines = append(outLines, line)
					continue
				}
				// create mapping name->index
				fidx := map[string]int{}
				for i, f := range stylesFormat {
					fidx[strings.ToLower(strings.TrimSpace(f))] = i
				}
				// apply scaling rules exactly per spec:
				// Fontsize (fontsize or size) -> * RY (round)
				if pos, ok := fidx["fontsize"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RY)
					}
				} else if pos, ok := fidx["size"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RY)
					}
				}
				// ScaleX -> * AR (special). Only apply if AR != 1 (non-trivial)
				if pos, ok := fidx["scalex"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						if !almostOne(AR) {
							parts[pos] = trimFloat(v * AR)
						} else {
							// preserve original if AR==1
							parts[pos] = trimFloat(v)
						}
					}
				}
				// ScaleY -> * RY
				if pos, ok := fidx["scaley"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = trimFloat(v * RY)
					}
				}
				// Outline -> * RY
				if pos, ok := fidx["outline"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = trimFloat(v * RY)
					}
				}
				// Shadow -> * RY
				if pos, ok := fidx["shadow"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = trimFloat(v * RY)
					}
				}
				// MarginL/MarginR -> * RX (round)
				if pos, ok := fidx["marginl"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RX)
					}
				}
				if pos, ok := fidx["marginr"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RX)
					}
				}
				// MarginV -> * RY (round)
				if pos, ok := fidx["marginv"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RY)
					}
				}
				// Reconstruct style line with ", " delimiter to preserve readability (close to Aegisub)
				outLines = append(outLines, "Style: "+strings.Join(parts, ","))
				continue
			}
			// pass through other style lines
			outLines = append(outLines, line)
			continue
		}

		// Events section
		if inEvents {
			lower := strings.ToLower(strings.TrimSpace(line))
			if strings.HasPrefix(lower, "format:") {
				idx := strings.Index(line, ":")
				fields := strings.Split(line[idx+1:], ",")
				for i := range fields {
					fields[i] = strings.TrimSpace(fields[i])
				}
				eventsFormat = fields
				outLines = append(outLines, line)
				continue
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "dialogue:") {
				if len(eventsFormat) == 0 {
					outLines = append(outLines, line)
					continue
				}
				idx := strings.Index(line, ":")
				if idx < 0 {
					outLines = append(outLines, line)
					continue
				}
				rest := strings.TrimSpace(line[idx+1:])
				parts := splitByCommasN(rest, len(eventsFormat))
				if len(parts) != len(eventsFormat) {
					outLines = append(outLines, line)
					continue
				}
				// map field names
				fidx := map[string]int{}
				for i, f := range eventsFormat {
					fidx[strings.ToLower(strings.TrimSpace(f))] = i
				}
				// MarginL/MarginR -> RX, MarginV -> RY (round ints)
				if pos, ok := fidx["marginl"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RX)
					}
				}
				if pos, ok := fidx["marginr"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RX)
					}
				}
				if pos, ok := fidx["marginv"]; ok {
					if v, err := strconv.ParseFloat(parts[pos], 64); err == nil {
						parts[pos] = intRound(v * RY)
					}
				}
				// find text field and process overrides/drawing
				textPos := -1
				if p, ok := fidx["text"]; ok {
					textPos = p
				} else {
					textPos = len(parts) - 1
				}
				parts[textPos] = processEventText(parts[textPos])
				outLines = append(outLines, "Dialogue: "+strings.Join(parts, ","))
				continue
			}
			outLines = append(outLines, line)
			continue
		}

		// default: pass through
		outLines = append(outLines, line)
	}

	return limenimizerASS(strings.Join(outLines, "\n"))
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
