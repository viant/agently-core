package style

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/viant/agently-core/protocol/ui/theme"
)

const MaxFontBytes = 1024 * 1024
const MaxTotalFontBytes = 8 * 1024 * 1024

func compileFonts(assets *os.Root, families []theme.FontFamily, revision io.Writer) ([]byte, map[string][]byte, error) {
	if len(families) == 0 {
		return nil, nil, nil
	}
	families = append([]theme.FontFamily(nil), families...)
	sort.Slice(families, func(i, j int) bool { return families[i].Role < families[j].Role })
	fontAssets := map[string][]byte{}
	total := 0
	var css bytes.Buffer
	for _, family := range families {
		faces := append([]theme.FontFace(nil), family.Faces...)
		sort.Slice(faces, func(i, j int) bool {
			left := faces[i].File + "\x00" + faces[i].Style + "\x00" + faces[i].Weight + "\x00" + faces[i].UnicodeRange
			right := faces[j].File + "\x00" + faces[j].Style + "\x00" + faces[j].Weight + "\x00" + faces[j].UnicodeRange
			return left < right
		})
		for _, face := range faces {
			if !validFontPath(face.File) {
				return nil, nil, fmt.Errorf("invalid font path %q", face.File)
			}
			data, err := readFile(assets, face.File, MaxFontBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: cannot read WOFF2 font within styles directory", face.File)
			}
			if len(data) < 4 || string(data[:4]) != "wOF2" {
				return nil, nil, fmt.Errorf("%s: invalid WOFF2 font", face.File)
			}
			total += len(data)
			if total > MaxTotalFontBytes {
				return nil, nil, fmt.Errorf("workspace fonts exceed %d bytes", MaxTotalFontBytes)
			}
			sum := sha256.Sum256(data)
			assetID := hex.EncodeToString(sum[:]) + ".woff2"
			fontAssets[assetID] = bytes.Clone(data)
			hashPart(revision, []byte(face.File))
			hashPart(revision, data)
			fmt.Fprintf(&css, "@font-face {\n  font-family: %s;\n  font-style: %s;\n  font-display: swap;\n  font-weight: %s;\n  src: url(%s) format(\"woff2\");\n",
				strconv.Quote(family.Name), face.Style, strings.Join(strings.Fields(face.Weight), " "), strconv.Quote("/v1/workspace/ui/fonts/"+assetID))
			if face.UnicodeRange != "" {
				fmt.Fprintf(&css, "  unicode-range: %s;\n", face.UnicodeRange)
			}
			css.WriteString("}\n")
		}
		fmt.Fprintf(&css, ".agently-application, .agently-workspace {\n  --agently-font-%s: %s, %s;\n}\n",
			family.Role, strconv.Quote(family.Name), fontFallback(family.Fallback))
	}
	return css.Bytes(), fontAssets, nil
}

func validFontPath(name string) bool {
	return name != "" && len(name) <= 1024 && fs.ValidPath(name) && path.Clean(name) == name &&
		!strings.ContainsAny(name, "\\:%?#\x00") && strings.EqualFold(path.Ext(name), ".woff2")
}

func fontFallback(value string) string {
	switch value {
	case "sans-serif", "serif", "monospace":
		return value
	default:
		return "system-ui, sans-serif"
	}
}
